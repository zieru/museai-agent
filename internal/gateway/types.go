package gateway

import "encoding/json"

type OpenAIChatRequest struct {
	Model    string          `json:"model"`
	Messages []OpenAIMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type OpenAIMessage struct {
	Role         string          `json:"role"`
	ContentStr   string          `json:"-"`
	ContentParts []ContentPart   `json:"-"`
	RawContent   json.RawMessage `json:"content"`
}

func (m *OpenAIMessage) UnmarshalJSON(data []byte) error {
	type Alias OpenAIMessage
	var aux Alias
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*m = OpenAIMessage(aux)

	// Try unmarshaling content as string
	var strVal string
	if err := json.Unmarshal(m.RawContent, &strVal); err == nil {
		m.ContentStr = strVal
		return nil
	}

	// Try unmarshaling content as array of multimodal parts
	var parts []ContentPart
	if err := json.Unmarshal(m.RawContent, &parts); err == nil {
		m.ContentParts = parts
		return nil
	}

	return nil
}

type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL string `json:"url"`
}

type AnthropicMessageRequest struct {
	Model    string             `json:"model"`
	Messages []AnthropicMessage `json:"messages"`
	Stream   bool               `json:"stream"`
}

type AnthropicMessage struct {
	Role       string          `json:"role"`
	ContentStr string          `json:"-"`
	RawContent json.RawMessage `json:"content"`
}

func (m *AnthropicMessage) UnmarshalJSON(data []byte) error {
	type Alias AnthropicMessage
	var aux Alias
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*m = AnthropicMessage(aux)

	var strVal string
	if err := json.Unmarshal(m.RawContent, &strVal); err == nil {
		m.ContentStr = strVal
		return nil
	}
	return nil
}
