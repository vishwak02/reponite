package processing

import "testing"

func TestDetectModulePath(t *testing.T) {
	cases := []struct {
		name  string
		files map[string][]byte
		want  string
	}{
		{
			name:  "go.mod module directive",
			files: map[string][]byte{"go.mod": []byte("module github.com/acme/billing\n\ngo 1.22\n")},
			want:  "github.com/acme/billing",
		},
		{
			name:  "package.json name",
			files: map[string][]byte{"package.json": []byte(`{"name": "@acme/web", "version": "1.0.0"}`)},
			want:  "@acme/web",
		},
		{
			name:  "pyproject PEP 621",
			files: map[string][]byte{"pyproject.toml": []byte("[build-system]\nrequires = [\"setuptools\"]\n\n[project]\nname = \"acme-svc\"\nversion = \"0.1\"\n")},
			want:  "acme-svc",
		},
		{
			name:  "pyproject poetry",
			files: map[string][]byte{"pyproject.toml": []byte("[tool.poetry]\nname = 'acme_poetry'\n")},
			want:  "acme_poetry",
		},
		{
			name:  "pom groupId:artifactId",
			files: map[string][]byte{"pom.xml": []byte(`<project><groupId>com.acme</groupId><artifactId>billing</artifactId></project>`)},
			want:  "com.acme:billing",
		},
		{
			name:  "none",
			files: map[string][]byte{"README.md": []byte("hi")},
			want:  "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := DetectModulePath(c.files)
			if c.want == "" {
				if ok {
					t.Fatalf("expected no module, got %q", got)
				}
				return
			}
			if !ok || got != c.want {
				t.Fatalf("DetectModulePath = %q,%v; want %q", got, ok, c.want)
			}
		})
	}
}

// A monorepo with a nested module must resolve to the ROOT module, not a
// deeper one — the path names the repo as a whole (§8B.2).
func TestDetectModulePathRootMost(t *testing.T) {
	files := map[string][]byte{
		"go.mod":            []byte("module github.com/acme/root\n"),
		"tools/sub/go.mod":  []byte("module github.com/acme/root/tools/sub\n"),
		"vendor/x/y/go.mod": []byte("module example.com/vendored\n"),
	}
	got, ok := DetectModulePath(files)
	if !ok || got != "github.com/acme/root" {
		t.Fatalf("root-most go.mod must win; got %q,%v", got, ok)
	}
}

// pom.xml with only an inherited (parent) groupId still yields the artifactId.
func TestPomArtifactOnly(t *testing.T) {
	files := map[string][]byte{
		"pom.xml": []byte(`<project><parent><groupId>com.parent</groupId></parent><artifactId>child-svc</artifactId></project>`),
	}
	got, ok := DetectModulePath(files)
	if !ok || got != "child-svc" {
		t.Fatalf("artifact-only pom must yield artifactId; got %q,%v", got, ok)
	}
}
