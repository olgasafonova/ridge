package java

import (
	"path/filepath"
	"testing"

	"github.com/olgasafonova/ridge/internal/analyzer/common"
	"github.com/olgasafonova/ridge/internal/model"
)

// importTargets analyzes the file at rel under root and returns its
// dependency edge targets keyed by label.
func importTargets(t *testing.T, root, rel string) map[string]string {
	t.Helper()
	_, edges, err := New().AnalyzeRooted(root, filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("AnalyzeRooted: %v", err)
	}
	targets := make(map[string]string)
	for _, e := range edges {
		if e.Type == model.EdgeDependency {
			targets[e.Label] = e.Target
		}
	}
	return targets
}

func TestAnalyzeRooted_ResolvesImportsOfProjectClasses(t *testing.T) {
	root := t.TempDir()
	writeJavaAt(t, root, "src/main/java/com/acme/web/Controller.java", "package com.acme.web;\npublic class Controller {}\n")
	writeJavaAt(t, root, "src/main/java/com/acme/util/Strings.java", "package com.acme.util;\npublic class Strings {}\n")
	writeJavaAt(t, root, "src/main/java/com/acme/model/Order.java", "package com.acme.model;\npublic class Order {}\n")
	writeJavaAt(t, root, "src/main/java/com/acme/App.java", `package com.acme;

import com.acme.web.Controller;
import static com.acme.util.Strings.blank;
import com.acme.model.Order.Line;
import com.acme.missing.Gone;
import java.util.List;
`)

	got := importTargets(t, root, "src/main/java/com/acme/App.java")
	want := map[string]string{
		"com.acme.web":          "mod:src/main/java/com/acme/web/Controller",
		"com.acme.util.Strings": "mod:src/main/java/com/acme/util/Strings",
		"com.acme.model.Order":  "mod:src/main/java/com/acme/model/Order",
		"com.acme.missing":      "import:com.acme.missing",
		"java.util":             "import:java.util",
	}
	for label, target := range want {
		if got[label] != target {
			t.Errorf("import labelled %q: target %q, want %q", label, got[label], target)
		}
	}
}

// A test class imports production code from the sibling source root, and one
// module of a multi-module build imports a class from another module.
func TestAnalyzeRooted_ResolvesAcrossSourceRoots(t *testing.T) {
	root := t.TempDir()
	writeJavaAt(t, root, "core/src/main/java/com/acme/core/Order.java", "package com.acme.core;\npublic class Order {}\n")
	writeJavaAt(t, root, "core/src/test/java/com/acme/core/OrderTest.java", "package com.acme.core;\nimport com.acme.core.Order;\nclass OrderTest {}\n")
	writeJavaAt(t, root, "api/src/main/java/com/acme/api/Api.java", "package com.acme.api;\nimport com.acme.core.Order;\nclass Api {}\n")

	for _, rel := range []string{"core/src/test/java/com/acme/core/OrderTest.java", "api/src/main/java/com/acme/api/Api.java"} {
		got := importTargets(t, root, rel)["com.acme.core"]
		if want := "mod:core/src/main/java/com/acme/core/Order"; got != want {
			t.Errorf("%s: target %q, want %q", rel, got, want)
		}
	}
}

// The importing file's own source root wins over a same-named class in
// another module.
func TestAnalyzeRooted_PrefersOwnSourceRoot(t *testing.T) {
	root := t.TempDir()
	writeJavaAt(t, root, "a/src/main/java/com/acme/Shared.java", "package com.acme;\nclass Shared {}\n")
	writeJavaAt(t, root, "b/src/main/java/com/acme/Shared.java", "package com.acme;\nclass Shared {}\n")
	writeJavaAt(t, root, "b/src/main/java/com/acme/User.java", "package com.acme;\nimport com.acme.Shared;\nclass User {}\n")

	got := importTargets(t, root, "b/src/main/java/com/acme/User.java")["com.acme"]
	if want := "mod:b/src/main/java/com/acme/Shared"; got != want {
		t.Errorf("target %q, want %q", got, want)
	}
}

// A file whose folder does not match its package, or that has no package,
// takes its own folder as its source root.
func TestAnalyzeRooted_PackageNotMatchingFolder(t *testing.T) {
	root := t.TempDir()
	writeJavaAt(t, root, "lib/Helper.java", "class Helper {}\n")
	writeJavaAt(t, root, "lib/com/x/Thing.java", "package com.x;\nclass Thing {}\n")
	writeJavaAt(t, root, "lib/Main.java", "package com.elsewhere;\nimport com.x.Thing;\nclass Main {}\n")
	writeJavaAt(t, root, "lib/Default.java", "import com.x.Thing;\nclass Default {}\n")

	for _, rel := range []string{"lib/Main.java", "lib/Default.java"} {
		got := importTargets(t, root, rel)["com.x"]
		if want := "mod:lib/com/x/Thing"; got != want {
			t.Errorf("%s: target %q, want %q", rel, got, want)
		}
	}
}

// Wildcard imports name a package, so they stay unresolved and keep the
// whole package as their label. Imports do not resolve without a scan root.
func TestAnalyze_WildcardAndUnrootedImportsStayUnresolved(t *testing.T) {
	root := t.TempDir()
	writeJavaAt(t, root, "src/main/java/com/acme/web/Controller.java", "package com.acme.web;\npublic class Controller {}\n")
	app := writeJavaAt(t, root, "src/main/java/com/acme/App.java", `package com.acme;
import com.acme.web.*;
import javax.persistence.*;
import com.acme.web.Controller;
`)

	got := importTargets(t, root, "src/main/java/com/acme/App.java")
	if got["javax.persistence"] != "import:javax.persistence" {
		t.Errorf("wildcard javax.persistence: target %q", got["javax.persistence"])
	}

	nodes, edges, err := New().Analyze(app)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !hasNodeOfType(nodes, model.NodeDatabase) {
		t.Errorf("wildcard javax.persistence import should classify as database")
	}
	for _, e := range edges {
		if resolvedWithoutRoot(e) {
			t.Errorf("unrooted import resolved to %q", e.Target)
		}
	}
}

// An import whose class path names a folder, or that climbs out of the scan
// root, is not a project file.
func TestAnalyzeRooted_IgnoresFoldersAndFilesOutsideRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "proj")
	writeJavaAt(t, parent, "com/acme/Outside.java", "package com.acme;\nclass Outside {}\n")
	writeJavaAt(t, root, "com/acme/Dir.java/Keep.java", "class Keep {}\n")
	writeJavaAt(t, root, "Main.java", "import com.acme.Dir;\nclass Main {}\n")

	got := importTargets(t, root, "Main.java")
	if got["com.acme"] != "import:com.acme" {
		t.Errorf("folder named like a class resolved to %q", got["com.acme"])
	}

	r := newImportResolver(common.SourceFile{Root: root, Path: filepath.Join(root, "Main.java")}, "")
	if r.isProjectFile(filepath.Join(parent, "com/acme/Outside.java")) {
		t.Errorf("file outside the scan root counted as a project file")
	}
}

// resolvedWithoutRoot reports whether an import of com.acme.web was resolved
// to a module although no scan root was given.
func resolvedWithoutRoot(e *model.Edge) bool {
	if e.Type != model.EdgeDependency || e.Label != "com.acme.web" {
		return false
	}
	return e.Target != "import:com.acme.web"
}
