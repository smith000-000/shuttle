package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type Provider string

const (
	ProviderNone       Provider = "none"
	ProviderBrave      Provider = "brave"
	ProviderPerplexity Provider = "perplexity"
)

type AvailabilityMode string

const (
	AvailabilityNone                      AvailabilityMode = "none"
	AvailabilityShuttle                   AvailabilityMode = "shuttle"
	AvailabilityRuntimeNative             AvailabilityMode = "runtime_native"
	AvailabilityRuntimeNativeWithFallback AvailabilityMode = "runtime_native_with_shuttle_fallback"
)

type Availability struct {
	Mode     AvailabilityMode `json:"mode,omitempty"`
	Runtime  string           `json:"runtime,omitempty"`
	Provider Provider         `json:"provider,omitempty"`
	Detail   string           `json:"detail,omitempty"`
}

type Request struct {
	Query string
}

type Result struct {
	Title   string
	URL     string
	Snippet string
}

type Status struct {
	Available bool
	Provider  Provider
	Detail    string
}

type Service interface {
	Status(context.Context) (Status, error)
	Search(context.Context, Request) ([]Result, error)
}

var (
	ErrNotConfigured  = errors.New("shuttle web search is not configured")
	ErrNotImplemented = errors.New("shuttle web search is not implemented yet")
)

type StubService struct {
	Provider Provider
}

func NormalizeProvider(value string) Provider {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "none", "off", "disabled":
		return ProviderNone
	case "brave", "brave-search":
		return ProviderBrave
	case "perplexity", "perplexity-search":
		return ProviderPerplexity
	default:
		return Provider(strings.ToLower(strings.TrimSpace(value)))
	}
}

func ProviderLabel(provider Provider) string {
	switch provider {
	case ProviderBrave:
		return "Brave"
	case ProviderPerplexity:
		return "Perplexity"
	default:
		return "none"
	}
}

func ShuttleAvailability(provider Provider) Availability {
	provider = NormalizeProvider(string(provider))
	if provider == ProviderNone {
		return Availability{
			Mode:   AvailabilityNone,
			Detail: "Shuttle builtin web search is not configured.",
		}
	}
	return Availability{
		Mode:     AvailabilityShuttle,
		Provider: provider,
		Detail:   fmt.Sprintf("Shuttle builtin web search is configured for %s, but execution is still a stub.", ProviderLabel(provider)),
	}
}

func RuntimeAvailability(runtime string, shuttleProvider Provider) Availability {
	runtime = strings.TrimSpace(runtime)
	if runtime == "" {
		return Availability{
			Mode:   AvailabilityNone,
			Detail: "No runtime-managed web search is available.",
		}
	}
	if NormalizeProvider(string(shuttleProvider)) != ProviderNone {
		return Availability{
			Mode:     AvailabilityRuntimeNativeWithFallback,
			Runtime:  runtime,
			Provider: NormalizeProvider(string(shuttleProvider)),
			Detail:   fmt.Sprintf("%s uses its own native web search. Shuttle %s search is also configured as a fallback product capability.", runtime, ProviderLabel(shuttleProvider)),
		}
	}
	return Availability{
		Mode:    AvailabilityRuntimeNative,
		Runtime: runtime,
		Detail:  fmt.Sprintf("%s uses its own native web search.", runtime),
	}
}

func (a Availability) Available() bool {
	return a.Mode != "" && a.Mode != AvailabilityNone
}

func (a Availability) Summary() string {
	switch a.Mode {
	case AvailabilityShuttle:
		if a.Provider != ProviderNone {
			return fmt.Sprintf("Shuttle-managed (%s)", ProviderLabel(a.Provider))
		}
		return "Shuttle-managed"
	case AvailabilityRuntimeNative:
		if strings.TrimSpace(a.Runtime) != "" {
			return fmt.Sprintf("Runtime-native (%s)", a.Runtime)
		}
		return "Runtime-native"
	case AvailabilityRuntimeNativeWithFallback:
		if strings.TrimSpace(a.Runtime) != "" && a.Provider != ProviderNone {
			return fmt.Sprintf("Runtime-native (%s) with Shuttle fallback (%s)", a.Runtime, ProviderLabel(a.Provider))
		}
		return "Runtime-native with Shuttle fallback"
	default:
		return "unavailable"
	}
}

func NewStubService(provider Provider) Service {
	return StubService{Provider: NormalizeProvider(string(provider))}
}

func (s StubService) Status(_ context.Context) (Status, error) {
	if s.Provider == ProviderNone {
		return Status{
			Available: false,
			Provider:  ProviderNone,
			Detail:    ErrNotConfigured.Error(),
		}, nil
	}
	return Status{
		Available: true,
		Provider:  s.Provider,
		Detail:    ErrNotImplemented.Error(),
	}, nil
}

func (s StubService) Search(_ context.Context, _ Request) ([]Result, error) {
	if s.Provider == ProviderNone {
		return nil, ErrNotConfigured
	}
	return nil, ErrNotImplemented
}
