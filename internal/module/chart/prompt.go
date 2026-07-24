package chart

import (
	"encoding/json"
	"fmt"
	"strings"
)

const systemPrompt = `你是一名数据分析师和前端可视化专家。
用户会提供分析需求与 CSV 原始数据。
你必须只输出一个合法 JSON 对象（不要 markdown 代码块、不要多余说明），结构严格为：
{
  "option": { ... ECharts V5 的 option 配置对象 ... },
  "conclusion": "详细的数据分析结论文本"
}
要求：
1. option 必须是可被 ECharts 直接使用的配置（含 series、xAxis/yAxis 或对应坐标系等合理字段）。
2. 根据数据选择合适的图表类型；若用户指定了图表类型，优先遵循。
3. conclusion 用中文，结论明确、有洞察。
4. 不要输出 JSON 以外的任何字符。`

// BuildUserPrompt 构造 user 消息。
func BuildUserPrompt(goal, chartType, csvData string) string {
	var b strings.Builder
	b.WriteString("分析需求：\n")
	userGoal := goal
	if strings.TrimSpace(chartType) != "" {
		userGoal += "，请使用" + chartType
	}
	b.WriteString(userGoal)
	b.WriteString("\n原始数据：\n")
	b.WriteString(csvData)
	b.WriteByte('\n')
	return b.String()
}

// ParseAIOutput 解析模型输出为 AIOutput；容忍 markdown 代码围栏。
func ParseAIOutput(raw string) (*AIOutput, error) {
	s := strings.TrimSpace(raw)
	s = stripCodeFence(s)

	var out AIOutput
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		// 尝试截取首尾大括号
		if start, end := strings.Index(s, "{"), strings.LastIndex(s, "}"); start >= 0 && end > start {
			if err2 := json.Unmarshal([]byte(s[start:end+1]), &out); err2 != nil {
				return nil, fmt.Errorf("parse ai json: %w", err)
			}
		} else {
			return nil, fmt.Errorf("parse ai json: %w", err)
		}
	}
	if out.Option == nil {
		return nil, fmt.Errorf("ai json missing option")
	}
	if strings.TrimSpace(out.Conclusion) == "" {
		return nil, fmt.Errorf("ai json missing conclusion")
	}
	return &out, nil
}

// OptionJSON 将 option 序列化为字符串落库。
func OptionJSON(option map[string]any) (string, error) {
	b, err := json.Marshal(option)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// ```json\n...\n```
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSpace(s)
	if strings.HasPrefix(strings.ToLower(s), "json") {
		s = strings.TrimSpace(s[4:])
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
