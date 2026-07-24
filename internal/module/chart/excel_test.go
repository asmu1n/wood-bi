package chart

import (
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestExcelToCSV(t *testing.T) {
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	_ = f.SetCellValue(sheet, "A1", "日期")
	_ = f.SetCellValue(sheet, "B1", "用户数")
	_ = f.SetCellValue(sheet, "A2", "1号")
	_ = f.SetCellValue(sheet, "B2", 10)
	_ = f.SetCellValue(sheet, "A3", "2号")
	_ = f.SetCellValue(sheet, "B3", 20)

	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}

	csv, err := ExcelToCSV(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	wantParts := []string{"日期", "用户数", "1号", "10", "2号", "20"}
	for _, p := range wantParts {
		if !contains(csv, p) {
			t.Fatalf("csv missing %q: %s", p, csv)
		}
	}
}
