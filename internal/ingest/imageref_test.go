package ingest

import "testing"

func TestClassify(t *testing.T) {
	cases := map[string]RefKind{
		"data:image/png;base64,AAA": Data,
		"http://x.com/a.png":        HTTP,
		"https://x.com/a.png":       HTTP,
		"//cdn.x.com/a.png":         HTTP,
		"/Users/me/a.png":           LocalAbsolute,
		"/static/a.png":             LocalAbsolute,
		"./a.png":                   LocalRelative,
		"../img/a.png":              LocalRelative,
		"a.png":                     LocalRelative,
	}
	for in, want := range cases {
		if got := Classify(in); got != want {
			t.Errorf("Classify(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestScanImageRefs_MarkdownInline(t *testing.T) {
	content := "# T\n\n![alt](./pic.png)\n\n![logo](https://x.com/l.png \"Title\")\n"
	refs := ScanImageRefs(content)
	if len(refs) != 2 {
		t.Fatalf("want 2 refs, got %d: %+v", len(refs), refs)
	}
	if refs[0].URL != "./pic.png" || refs[0].Kind != LocalRelative {
		t.Errorf("ref0 = %+v", refs[0])
	}
	if refs[1].URL != "https://x.com/l.png" || refs[1].Kind != HTTP {
		t.Errorf("ref1 = %+v (title must not be captured)", refs[1])
	}
	// Offsets must point at the URL token.
	if content[refs[0].Start:refs[0].End] != "./pic.png" {
		t.Errorf("ref0 offsets wrong: %q", content[refs[0].Start:refs[0].End])
	}
}

func TestScanImageRefs_HTMLImg(t *testing.T) {
	content := `<img src="./a.png" alt="x"> and <img alt='y' src='/abs/b.png'>`
	refs := ScanImageRefs(content)
	if len(refs) != 2 {
		t.Fatalf("want 2, got %d: %+v", len(refs), refs)
	}
	if refs[0].URL != "./a.png" || refs[0].Syntax != HTMLImg {
		t.Errorf("ref0 = %+v", refs[0])
	}
	if refs[1].URL != "/abs/b.png" || refs[1].Kind != LocalAbsolute {
		t.Errorf("ref1 = %+v", refs[1])
	}
}

func TestScanImageRefs_IgnoresMarkdownCode(t *testing.T) {
	content := "```markdown\n![code](./code.png)\n<img src=\"./code-html.png\">\n```\n" +
		"real ![ok](./ok.png) and `![inline](./inline.png)`\n" +
		"~~~\n![tilde](./tilde.png)\n~~~\n"
	refs := ScanImageRefs(content)
	if len(refs) != 1 {
		t.Fatalf("want 1 real ref, got %d: %+v", len(refs), refs)
	}
	if refs[0].URL != "./ok.png" {
		t.Fatalf("URL = %q, want ./ok.png", refs[0].URL)
	}
}

func TestRewriteRefs_OffsetAnchored(t *testing.T) {
	// The literal path also appears in body prose; only the image ref must change.
	content := "see ./pic.png below\n\n![p](./pic.png)\n"
	refs := ScanImageRefs(content)
	out := RewriteRefs(content, refs, map[string]string{"./pic.png": "https://i.pura.so/u/abc.png"})
	want := "see ./pic.png below\n\n![p](https://i.pura.so/u/abc.png)\n"
	if out != want {
		t.Errorf("RewriteRefs:\n got=%q\nwant=%q", out, want)
	}
}

func TestRewriteRefs_DedupSameTarget(t *testing.T) {
	content := "![a](./x.png) ![b](./x.png)"
	refs := ScanImageRefs(content)
	out := RewriteRefs(content, refs, map[string]string{"./x.png": "https://i.pura.so/u/x.png"})
	want := "![a](https://i.pura.so/u/x.png) ![b](https://i.pura.so/u/x.png)"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestRewriteRefs_NoMapNoChange(t *testing.T) {
	content := "![a](https://x.com/a.png)"
	refs := ScanImageRefs(content)
	if out := RewriteRefs(content, refs, map[string]string{}); out != content {
		t.Errorf("expected unchanged, got %q", out)
	}
}
