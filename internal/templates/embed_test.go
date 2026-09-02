package templates

import (
	"strings"
	"testing"
)

func TestEmbeddedTemplatePresent(t *testing.T) {
	content := strings.TrimSpace(EmbeddedTemplate)
	if content == "" {
		t.Fatal("embedded template is empty")
	}

	if !strings.Contains(content, "<!DOCTYPE html>") {
		t.Fatal("embedded template does not look like an HTML document")
	}
}

func TestEmbeddedTemplateIncludesWorkbookThemeControls(t *testing.T) {
	for _, expected := range []string{
		`data-theme`,
		`id="themeToggle"`,
		`id="progressFill"`,
		`exam-workbook-theme`,
		`prefers-reduced-motion`,
	} {
		if !strings.Contains(EmbeddedTemplate, expected) {
			t.Errorf("embedded workbook template is missing %q", expected)
		}
	}
}
