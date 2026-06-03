package model

import (
	"context"
	"fmt"
	"strings"

	"github.com/ssbsunshengbo/minicode-go/internal/config"
	"github.com/ssbsunshengbo/minicode-go/internal/message"
	"github.com/ssbsunshengbo/minicode-go/internal/tools"
)

func NewFromRuntime(runtime config.Runtime, registry *tools.Registry) (message.Model, error) {
	provider := strings.ToLower(strings.TrimSpace(runtime.Provider))
	if provider == "" {
		provider = "anthropic"
	}
	switch provider {
	case "mock":
		return Mock{}, nil
	case "anthropic", "anthropic-compatible":
		return NewAnthropic(func(context.Context) (config.Runtime, error) {
			return runtime, nil
		}, registry), nil
	case "openai", "openai-compatible":
		return NewOpenAI(func(context.Context) (config.Runtime, error) {
			return runtime, nil
		}, registry), nil
	default:
		return nil, fmt.Errorf("unsupported model provider: %s", runtime.Provider)
	}
}
