package typescript

import (
	"net/url"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/olgasafonova/ridge/internal/model"
)

// The URL of an HTTP call is often not written at the call:
//
//	const url = `${API}/search?q=${encodeURIComponent(q)}`;
//	const res = await fetch(url);
//
// urlReader reads such a URL from literals, templates, "+" concatenation,
// URL objects (new URL(...), url.toString(), url.href) and bindings in the
// same file: a const, or a let or var nothing assigns to again. A part only
// known at run time becomes model.RouteWildcard.

// maxURLHops bounds how many const bindings one URL is followed through.
const maxURLHops = 4

// urlReader evaluates expressions of one file as URLs.
type urlReader struct {
	src  []byte
	hops int // const bindings it may still follow
}

// value evaluates expr as a URL. It returns "" when nothing of it is known
// statically.
func (r urlReader) value(expr *sitter.Node) string {
	if eval, known := urlEvaluators[expr.Type()]; known {
		return eval(r, expr)
	}
	return ""
}

// urlEvaluators maps the expression types a URL is read from to how each is
// evaluated.
var urlEvaluators map[string]func(urlReader, *sitter.Node) string

func init() {
	urlEvaluators = map[string]func(urlReader, *sitter.Node) string{
		"string":                   urlReader.literal,
		"template_string":          urlReader.template,
		"binary_expression":        urlReader.concat,
		"parenthesized_expression": urlReader.inner,
		"template_substitution":    urlReader.inner,
		"identifier":               urlReader.bound,
		"new_expression":           urlReader.newURL,
		"call_expression":          urlReader.stringified,
		"member_expression":        urlReader.href,
	}
}

// literal evaluates a string literal.
func (r urlReader) literal(str *sitter.Node) string {
	return extractStringLiteral(str, r.src)
}

// newURL evaluates new URL(input) and new URL(input, base). A path is
// resolved against a base that is a known absolute URL or the page's own
// origin; against any other base it is unknown.
func (r urlReader) newURL(expr *sitter.Node) string {
	args := r.urlConstructorArgs(expr)
	if args == nil {
		return ""
	}
	input := r.value(args.NamedChild(0))
	if args.NamedChildCount() == 1 || strings.Contains(input, "://") {
		return input
	}
	return r.resolveAgainst(input, args.NamedChild(1))
}

// urlConstructorArgs returns the arguments of new URL(...), or nil for any
// other constructor or a call with none.
func (r urlReader) urlConstructorArgs(expr *sitter.Node) *sitter.Node {
	ctor := expr.ChildByFieldName("constructor")
	args := expr.ChildByFieldName("arguments")
	if anyNil(ctor, args) || ctor.Content(r.src) != "URL" {
		return nil
	}
	if args.NamedChildCount() == 0 {
		return nil
	}
	return args
}

// resolveAgainst resolves the path input against the base expression.
func (r urlReader) resolveAgainst(input string, base *sitter.Node) string {
	if !isSameOriginPath(input) {
		return ""
	}
	if pageOrigins[base.Content(r.src)] {
		return input
	}
	b, err := url.Parse(r.value(base))
	if err != nil || !b.IsAbs() {
		return ""
	}
	return b.Scheme + "://" + b.Host + input
}

// pageOrigins are base expressions that stand for the page's own origin.
var pageOrigins = map[string]bool{
	"location.origin": true, "location.href": true,
	"window.location.origin": true, "window.location.href": true,
}

// stringified evaluates x.toString() and String(x) as the value of x.
func (r urlReader) stringified(call *sitter.Node) string {
	fn := call.ChildByFieldName("function")
	args := call.ChildByFieldName("arguments")
	if anyNil(fn, args) {
		return ""
	}
	if fn.Content(r.src) == "String" && args.NamedChildCount() == 1 {
		return r.value(args.NamedChild(0))
	}
	if fn.Type() != "member_expression" || args.NamedChildCount() != 0 {
		return ""
	}
	return r.property(fn, "toString")
}

// href evaluates url.href as the value of url.
func (r urlReader) href(member *sitter.Node) string {
	return r.property(member, "href")
}

// property evaluates obj.<name> as the value of obj, and anything else as
// unknown.
func (r urlReader) property(member *sitter.Node, name string) string {
	obj := member.ChildByFieldName("object")
	prop := member.ChildByFieldName("property")
	if anyNil(obj, prop) || prop.Content(r.src) != name {
		return ""
	}
	return r.value(obj)
}

// template evaluates a template literal. A ${...} that is not itself a known
// string becomes a wildcard.
func (r urlReader) template(tpl *sitter.Node) string {
	var sb strings.Builder
	for i := 0; i < int(tpl.ChildCount()); i++ {
		switch child := tpl.Child(i); child.Type() {
		case "template_substitution":
			sb.WriteString(orWildcard(r.value(child)))
		case "`":
		default:
			sb.WriteString(child.Content(r.src))
		}
	}
	return sb.String()
}

// inner evaluates the expression wrapped by parentheses or by ${...}.
func (r urlReader) inner(wrapper *sitter.Node) string {
	if wrapper.NamedChildCount() == 0 {
		return ""
	}
	return r.value(wrapper.NamedChild(0))
}

// concat evaluates "a + b". It is "" when neither side is known.
func (r urlReader) concat(expr *sitter.Node) string {
	left, right := expr.ChildByFieldName("left"), expr.ChildByFieldName("right")
	op := expr.ChildByFieldName("operator")
	if anyNil(left, right, op) || op.Content(r.src) != "+" {
		return ""
	}
	l, rv := r.value(left), r.value(right)
	if l == "" && rv == "" {
		return ""
	}
	return orWildcard(l) + orWildcard(rv)
}

// bound evaluates a name through the const that declares it.
func (r urlReader) bound(name *sitter.Node) string {
	if r.hops == 0 {
		return ""
	}
	value := nameUse{node: name, src: r.src}.constValue()
	if value == nil {
		return ""
	}
	return urlReader{src: r.src, hops: r.hops - 1}.value(value)
}

func orWildcard(s string) string {
	if s == "" {
		return model.RouteWildcard
	}
	return s
}

func anyNil(nodes ...*sitter.Node) bool {
	for _, n := range nodes {
		if n == nil {
			return true
		}
	}
	return false
}

// nameUse is one use of a name in a file.
type nameUse struct {
	node *sitter.Node
	src  []byte
}

func (u nameUse) is(n *sitter.Node) bool {
	return n != nil && n.Content(u.src) == u.node.Content(u.src)
}

// constValue finds the value the name was given by a declaration that is in
// scope at the use: a const, or a let or var that nothing in its scope
// assigns to again. It returns nil when the name is a parameter or loop
// variable, is assigned to after its declaration, or is not declared in this
// file.
func (u nameUse) constValue() *sitter.Node {
	for scope := u.node.Parent(); scope != nil; scope = scope.Parent() {
		if value, declared := u.declaredIn(scope); declared {
			return value
		}
		if u.reboundBy(scope) {
			return nil
		}
	}
	return nil
}

// declaredIn looks among the statements directly inside scope, before the
// use, for a declaration of the name. value is nil when the name may hold
// something else by the time it is used.
func (u nameUse) declaredIn(scope *sitter.Node) (value *sitter.Node, declared bool) {
	for i := 0; i < int(scope.NamedChildCount()); i++ {
		stmt := scope.NamedChild(i)
		if stmt.StartByte() >= u.node.StartByte() {
			break
		}
		decl := variableDeclaration(stmt)
		if decl == nil {
			continue
		}
		if d := u.declarator(decl); d != nil {
			return u.stableInitializer(scope, decl, d), true
		}
	}
	return nil, false
}

// declarator returns the part of decl that declares the name, or nil.
func (u nameUse) declarator(decl *sitter.Node) *sitter.Node {
	for _, d := range namedChildren(decl, "variable_declarator") {
		if u.is(d.ChildByFieldName("name")) {
			return d
		}
	}
	return nil
}

// variableDeclaration returns stmt as a const/let/var declaration, looking
// through "export", or nil for any other statement.
func variableDeclaration(stmt *sitter.Node) *sitter.Node {
	if stmt.Type() == "export_statement" {
		stmt = stmt.ChildByFieldName("declaration")
	}
	if stmt == nil {
		return nil
	}
	if t := stmt.Type(); t != "lexical_declaration" && t != "variable_declaration" {
		return nil
	}
	return stmt
}

// stableInitializer returns the declarator's value when decl is a const, or
// a let or var nothing in scope assigns to again.
func (u nameUse) stableInitializer(scope, decl, declarator *sitter.Node) *sitter.Node {
	if decl.Child(0).Type() != "const" && u.assignedIn(scope) {
		return nil
	}
	return declarator.ChildByFieldName("value")
}

// assignmentTargets maps the node types that write to a variable to the
// field naming what they write: x = ..., x += ..., x++.
var assignmentTargets = map[string]string{
	"assignment_expression":           "left",
	"augmented_assignment_expression": "left",
	"update_expression":               "argument",
}

// assignedIn reports whether anything under n writes to the name or to one
// of its properties (url.pathname = ...).
func (u nameUse) assignedIn(n *sitter.Node) bool {
	if field, writes := assignmentTargets[n.Type()]; writes && u.writtenBy(n.ChildByFieldName(field)) {
		return true
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if u.assignedIn(n.NamedChild(i)) {
			return true
		}
	}
	return false
}

// writtenBy reports whether an assignment target is the name or a property
// of it.
func (u nameUse) writtenBy(target *sitter.Node) bool {
	for target != nil && target.Type() == "member_expression" {
		target = target.ChildByFieldName("object")
	}
	return u.is(target)
}

// binderFields are the fields through which a scope node gives names a new
// meaning: function parameters, a catch parameter, a for-in/of variable.
var binderFields = []string{"parameters", "parameter", "left"}

// bindingScopes are the node types whose binder fields are checked.
var bindingScopes = map[string]bool{
	"function_declaration": true, "function_expression": true, "function": true,
	"arrow_function": true, "method_definition": true, "generator_function": true,
	"generator_function_declaration": true, "catch_clause": true, "for_in_statement": true,
}

// reboundBy reports whether scope gives the name a new meaning, which hides
// any declaration of it further out.
func (u nameUse) reboundBy(scope *sitter.Node) bool {
	if !bindingScopes[scope.Type()] {
		return false
	}
	for _, field := range binderFields {
		if binder := scope.ChildByFieldName(field); binder != nil && u.mentionedIn(binder) {
			return true
		}
	}
	return false
}

// mentionedIn reports whether an identifier with the name occurs under n.
func (u nameUse) mentionedIn(n *sitter.Node) bool {
	if n.Type() == "identifier" && u.is(n) {
		return true
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if u.mentionedIn(n.NamedChild(i)) {
			return true
		}
	}
	return false
}
