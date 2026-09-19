package llm

func NewOpenRouter(apiKey string, headers map[string]string) Provider {
	merged := map[string]string{
		"X-Title": "Rocina",
	}
	for k, v := range headers {
		merged[k] = v
	}
	return NewOpenAI("openrouter", "https://openrouter.ai/api/v1", apiKey, merged)
}
