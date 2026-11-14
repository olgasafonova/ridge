package typescript

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/olgasafonova/ridge/internal/model"
)

// The URL of an HTTP call is often not written at the call:
//
//	const url = `${API}/search?q=${encodeURIComponent(q)}`;
//	const res = await fetch(url);
//
// urlReader reads such a URL from literals, templates, "+" concatenation and
// const bindings in the same file. A part only known at run time becomes
// model.RouteWildcard.

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
	switch expr.Type() {
	case "string":
		return extractStringLiteral(expr, r.src)
	case "template_string":
		return r.template(expr)
	case "binary_expression":
		return r.concat(expr)
	case "parenthesized_expression", "template_substitution":
		return r.inner(expr)
	case "identifier":
		return r.bound(expr)
	}
	return ""
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

// constValue finds the value the name was given by a const declaration that
// is in scope at the use. It returns nil when the name is a parameter or loop
// variable, is declared with let or var (it may have been reassigned), or is
// not declared in this file.
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
// use, for a declaration of the name. value is nil unless it is a const.
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
			return constInitializer(decl, d), true
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

// constInitializer returns the declarator's value when decl is a const.
func constInitializer(decl, declarator *sitter.Node) *sitter.Node {
	if decl.Child(0).Type() != "const" {
		return nil
	}
	return declarator.ChildByFieldName("value")
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
