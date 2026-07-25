package chart

import (
	"bytes"
	"strings"

	"wood-bi/internal/pkg/response"

	"github.com/xuri/excelize/v2"
)

// excelToCSV 将 xlsx 首个工作表转为 CSV 文本（首行为表头）。
func excelToCSV(fileBytes []byte) (string, error) {
	if len(fileBytes) == 0 {
		return "", response.NewBizErrorWithDetail(response.ParamsError, "文件为空")
	}
	f, err := excelize.OpenReader(bytes.NewReader(fileBytes))
	if err != nil {
		return "", response.NewBizErrorWithDetail(response.ParamsError, "表格解析失败")
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return "", response.NewBizErrorWithDetail(response.ParamsError, "表格无工作表")
	}

	rows, err := f.GetRows(sheets[0])
	if err != nil {
		return "", response.NewBizErrorWithDetail(response.ParamsError, "表格读取失败")
	}

	var b strings.Builder
	for _, row := range rows {
		// 去掉行尾空单元格，减少噪音
		for len(row) > 0 && strings.TrimSpace(row[len(row)-1]) == "" {
			row = row[:len(row)-1]
		}
		if len(row) == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		for j, cell := range row {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteString(escapeCSVCell(cell))
		}
	}

	csv := strings.TrimSpace(b.String())
	if csv == "" {
		return "", response.NewBizErrorWithDetail(response.ParamsError, "表格数据为空")
	}
	return csv, nil
}

func escapeCSVCell(s string) string {
	s = strings.TrimSpace(s)
	if strings.ContainsAny(s, ",\"\n\r") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}
