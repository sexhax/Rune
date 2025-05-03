package api

import (
	"github.com/jeandeaual/go-locale"
)

func mustGetSystemLocale() string {
	userLocales, err := locale.GetLocales()

	if err != nil {
		return "en-US" // Default to English (US) if locale detection fails
	}

	if len(userLocales) == 0 {
		return "en-US"
	}

	return userLocales[0]
}
