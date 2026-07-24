package chart

import "testing"

func TestParseAIOutput(t *testing.T) {
	raw := `{
  "option": {"title": {"text": "增长"}, "series": [{"type": "line", "data": [1,2,3]}]},
  "conclusion": "用户呈上升趋势"
}`
	out, err := ParseAIOutput(raw)
	if err != nil {
		t.Fatal(err)
	}
	if out.Conclusion != "用户呈上升趋势" {
		t.Fatalf("conclusion=%q", out.Conclusion)
	}
	if out.Option["title"] == nil {
		t.Fatal("missing option.title")
	}
}

func TestParseAIOutputCodeFence(t *testing.T) {
	raw := "```json\n{\"option\":{\"x\":1},\"conclusion\":\"ok\"}\n```"
	out, err := ParseAIOutput(raw)
	if err != nil {
		t.Fatal(err)
	}
	if out.Conclusion != "ok" {
		t.Fatalf("conclusion=%q", out.Conclusion)
	}
}

func TestParseAIOutputMissing(t *testing.T) {
	if _, err := ParseAIOutput(`{"option":{}}`); err == nil {
		t.Fatal("expected error for missing conclusion")
	}
	if _, err := ParseAIOutput(`{"conclusion":"x"}`); err == nil {
		t.Fatal("expected error for missing option")
	}
}

func TestBuildUserPrompt(t *testing.T) {
	p := BuildUserPrompt("分析增长", "折线图", "日期,数\n1,10")
	if p == "" || !containsAll(p, "分析增长", "折线图", "日期,数") {
		t.Fatalf("prompt=%q", p)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !contains(s, p) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && (func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		})()))
}
