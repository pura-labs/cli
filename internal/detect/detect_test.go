package detect

import "testing"

func TestFromExtension(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
	}{
		{"markdown .md", "report.md", "markdown"},
		{"markdown .markdown", "report.markdown", "markdown"},
		{"markdown .txt", "notes.txt", "markdown"},
		{"csv", "data.csv", "csv"},
		{"json", "config.json", "json"},
		{"html", "page.html", "html"},
		{"htm", "page.htm", "html"},
		{"svg", "icon.svg", "svg"},
		{"js as canvas", "sketch.js", "canvas"},
		{"unknown ext", "file.xyz", ""},
		{"no ext", "file", ""},
		{"empty", "", ""},
		{"case insensitive", "FILE.MD", "markdown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fromExtension(tt.filename)
			if got != tt.want {
				t.Errorf("fromExtension(%q) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}

func TestFromContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"json object", `{"key": "value"}`, "json"},
		{"json array", `[{"a":1},{"a":2}]`, "json"},
		{"invalid json with brace", "{not json}", "markdown"},
		{"svg", `<svg width="100"></svg>`, "svg"},
		{"svg with xml", "<?xml version=\"1.0\"?>\n<svg></svg>", "svg"},
		{"html doctype", "<!DOCTYPE html><html></html>", "html"},
		{"html tag", "<html><body></body></html>", "html"},
		{"csv commas", "name,age,city\nAlice,30,NYC\nBob,25,LA", "csv"},
		{"tsv tabs", "name\tage\nAlice\t30", "csv"},
		{"single line not csv", "just, some, text", "markdown"},
		{"markdown heading", "# Hello World\n\nSome text.", "markdown"},
		{"empty", "", "markdown"},
		{"whitespace json", "  \n  {\"key\": \"value\"}", "json"},
		{"unicode", "# 你好世界\n\n内容", "markdown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fromContent(tt.content)
			if got != tt.want {
				t.Errorf("fromContent(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}

func TestType(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		content  string
		want     string
	}{
		{"extension wins", "data.csv", "# Heading", "csv"},
		{"content fallback", "", `{"key":"value"}`, "json"},
		{"unknown ext falls to content", "file.xyz", "# Heading", "markdown"},
		{"no info defaults markdown", "", "hello", "markdown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Type(tt.filename, tt.content)
			if got != tt.want {
				t.Errorf("Type(%q, %q) = %q, want %q", tt.filename, tt.content, got, tt.want)
			}
		})
	}
}
