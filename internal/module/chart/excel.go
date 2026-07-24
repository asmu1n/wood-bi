package chart

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

// ExcelToCSV 将 xlsx 首个工作表转为 CSV 文本（首行为表头）。
func ExcelToCSV(fileBytes []byte) (string, error) {
	if len(fileBytes) == 0 {
		return "", fmt.Errorf("empty excel file")
	}
	f, err := excelize.OpenReader(bytes.NewReader(fileBytes))
	if err != nil {
		return "", fmt.Errorf("open excel: %w", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return "", fmt.Errorf("excel has no sheets")
	}

	rows, err := f.GetRows(sheets[0])
	if err != nil {
		return "", fmt.Errorf("read rows: %w", err)
	}
	if len(rows) == 0 {
		return "", nil
	}

	var b strings.Builder
	for i, row := range rows {
		// 去掉行尾空单元格，减少噪音
		for len(row) > 0 && strings.TrimSpace(row[len(row)-1]) == "" {
			row = row[:len(row)-1]
		}
		if len(row) == 0 {
			continue
		}
		for j, cell := range row {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteString(escapeCSVCell(cell))
		}
		if i < len(rows)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String(), nil
}

func escapeCSVCell(s string) string {
	s = strings.TrimSpace(s)
	if strings.ContainsAny(s, ",\"\n\r") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}
