package search

import (
	"context"
	"errors"
	"testing"
)

func TestShuttleAvailabilityDefaultsToUnavailable(t *testing.T) {
	availability := ShuttleAvailability(ProviderNone)
	if availability.Mode != AvailabilityNone {
		t.Fatalf("expected unavailable mode, got %q", availability.Mode)
	}
	if availability.Available() {
		t.Fatal("expected unavailable search capability")
	}
}

func TestRuntimeAvailabilityUsesFallbackWhenConfigured(t *testing.T) {
	availability := RuntimeAvailability("pi", ProviderBrave)
	if availability.Mode != AvailabilityRuntimeNativeWithFallback {
		t.Fatalf("expected runtime fallback mode, got %q", availability.Mode)
	}
	if availability.Provider != ProviderBrave {
		t.Fatalf("expected brave fallback, got %q", availability.Provider)
	}
}

func TestStubServiceSearchReturnsConfiguredState(t *testing.T) {
	service := NewStubService(ProviderPerplexity)
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !status.Available || status.Provider != ProviderPerplexity {
		t.Fatalf("unexpected status %#v", status)
	}
	if _, err := service.Search(context.Background(), Request{Query: "latest"}); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("expected not implemented, got %v", err)
	}
}
