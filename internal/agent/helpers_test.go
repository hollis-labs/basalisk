package agent

import "github.com/hollis-labs/libs/llm/core/llmtypes"

func blocksText(s string) []llmtypes.ContentBlock {
	return []llmtypes.ContentBlock{{Type: "text", Text: s}}
}

func toolUse(id, name string, input *map[string]any) llmtypes.ContentBlock {
	return llmtypes.ContentBlock{Type: "tool_use", ID: id, Name: name, Input: input}
}
