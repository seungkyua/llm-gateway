package apierror

import "testing"

func TestNewShouldBuildOpenAICompatibleErrorBody(t *testing.T) {
	body := New("missing api key", TypeAuthenticationError, CodeInvalidAPIKey)

	if body.Error.Message != "missing api key" {
		t.Errorf("expected message to be preserved, got %q", body.Error.Message)
	}
	if body.Error.Type != "authentication_error" {
		t.Errorf("expected type authentication_error, got %q", body.Error.Type)
	}
	if body.Error.Code != "invalid_api_key" {
		t.Errorf("expected code invalid_api_key, got %q", body.Error.Code)
	}
}
