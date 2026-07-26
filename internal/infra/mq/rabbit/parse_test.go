package rabbit

import "testing"

func TestParseChartID(t *testing.T) {
	id, err := parseChartID([]byte(`{"chartId":42}`))
	if err != nil || id != 42 {
		t.Fatalf("json: id=%d err=%v", id, err)
	}
	id, err = parseChartID([]byte(` 99 `))
	if err != nil || id != 99 {
		t.Fatalf("plain: id=%d err=%v", id, err)
	}
	if _, err := parseChartID([]byte(``)); err == nil {
		t.Fatal("expected empty error")
	}
	if _, err := parseChartID([]byte(`{"chartId":0}`)); err == nil {
		t.Fatal("expected zero id error")
	}
}
