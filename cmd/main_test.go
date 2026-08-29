package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"examtopics-downloader/internal/fetch"
)

func TestParseMenuCommand(t *testing.T) {
	tests := []struct {
		input   string
		command string
		value   string
		ok      bool
	}{
		{input: "/scan", command: "scan", ok: true},
		{input: " /ALL ", command: "all", ok: true},
		{input: "/refresh", command: "refresh", ok: true},
		{input: "/reload", command: "refresh", ok: true},
		{input: "/", command: "clear", ok: true},
		{input: "/clear", command: "clear", ok: true},
		{input: "/filter AZ-305", command: "filter", value: "AZ-305", ok: true},
		{input: "/f microsoft", command: "filter", value: "microsoft", ok: true},
		{input: "/ Azure ", command: "filter", value: "Azure", ok: true},
		{input: "2", command: "", ok: false},
	}
	for _, test := range tests {
		command, value, ok := parseMenuCommand(test.input)
		if command != test.command || value != test.value || ok != test.ok {
			t.Errorf("parseMenuCommand(%q) = (%q, %q, %v), want (%q, %q, %v)", test.input, command, value, ok, test.command, test.value, test.ok)
		}
	}
}

func TestProviderMenuPlainTextFilterSelectsFilteredResult(t *testing.T) {
	selected, err := promptSelectionWithRefresh(
		bufioReader("micro\n1\n"), "Providers",
		func() []string { return []string{"amazon", "microsoft", "oracle"} }, strings.ToUpper,
	)
	if err != nil || selected != "microsoft" {
		t.Fatalf("selected = %q, err = %v", selected, err)
	}
}

func TestProviderMenuExplicitAndLegacyFilters(t *testing.T) {
	for name, input := range map[string]string{
		"explicit": "/filter oracle\n1\n",
		"short":    "/f oracle\n1\n",
		"legacy":   "/oracle\n1\n",
	} {
		t.Run(name, func(t *testing.T) {
			selected, err := promptSelectionWithRefresh(
				bufioReader(input), "Providers",
				func() []string { return []string{"amazon", "oracle"} }, strings.ToUpper,
			)
			if err != nil || selected != "oracle" {
				t.Fatalf("selected = %q, err = %v", selected, err)
			}
		})
	}
}

func TestProviderMenuClearAndReload(t *testing.T) {
	calls := 0
	loader := func() []string {
		calls++
		if calls == 1 {
			return []string{"amazon", "microsoft"}
		}
		return []string{"new-provider"}
	}
	selected, err := promptSelectionWithRefresh(bufioReader("micro\n/clear\n/reload\n1\n"), "Providers", loader, strings.ToUpper)
	if err != nil || selected != "new-provider" || calls != 2 {
		t.Fatalf("selected = %q, calls = %d, err = %v", selected, calls, err)
	}
}

func TestProviderMenuInvalidNumberThenSelection(t *testing.T) {
	selected, err := promptSelectionWithRefresh(
		bufioReader("99\n2\n"), "Providers",
		func() []string { return []string{"amazon", "microsoft"} }, strings.ToUpper,
	)
	if err != nil || selected != "microsoft" {
		t.Fatalf("selected = %q, err = %v", selected, err)
	}
}

func TestExamMenuFilterClearAndSelect(t *testing.T) {
	selected, err := promptExamMenu(
		bufioReader("az-305\n/clear\n/filter az-104\n1\n"), "microsoft",
		[]string{"az-104", "az-305"}, nil,
		func() ([]string, error) { return nil, nil },
		func() (fetch.ProviderIndex, error) { return fetch.ProviderIndex{}, nil },
	)
	if err != nil || selected != "az-104" {
		t.Fatalf("selected = %q, err = %v", selected, err)
	}
}

func TestExamMenuRefreshReloadsOfficialOptions(t *testing.T) {
	calls := 0
	selected, err := promptExamMenu(
		bufioReader("/refresh\nsc-100\n1\n"), "microsoft", []string{"az-104"}, nil,
		func() ([]string, error) { calls++; return []string{"sc-100"}, nil },
		func() (fetch.ProviderIndex, error) { return fetch.ProviderIndex{}, nil },
	)
	if err != nil || selected != "sc-100" || calls != 1 {
		t.Fatalf("selected = %q, calls = %d, err = %v", selected, calls, err)
	}
}

func TestExamMenuScanAddsDiscussionOptions(t *testing.T) {
	scans := 0
	selected, err := promptExamMenu(
		bufioReader("/scan\naz-305\n1\n"), "microsoft", []string{"az-104"}, nil,
		func() ([]string, error) { return nil, nil },
		func() (fetch.ProviderIndex, error) {
			scans++
			return fetch.ProviderIndex{ExamSlugs: []string{"az-305"}, Complete: true}, nil
		},
	)
	if err != nil || selected != "az-305" || scans != 1 {
		t.Fatalf("selected = %q, scans = %d, err = %v", selected, scans, err)
	}
}

func TestExamMenuAllReturnsSentinelWithoutScanning(t *testing.T) {
	scans := 0
	selected, err := promptExamMenu(
		bufioReader("/all\n"), "microsoft", []string{"az-104"}, nil,
		func() ([]string, error) { return nil, nil },
		func() (fetch.ProviderIndex, error) { scans++; return fetch.ProviderIndex{}, nil },
	)
	if err != nil || selected != "all-discussions" || scans != 0 {
		t.Fatalf("selected = %q, scans = %d, err = %v", selected, scans, err)
	}
}

func bufioReader(input string) *bufio.Reader {
	return bufio.NewReader(strings.NewReader(input))
}

func TestFilterOptionsPreservesRawSelectionIndex(t *testing.T) {
	options := []selectionOption{
		{RawIndex: 0, Label: "az-104"},
		{RawIndex: 1, Label: "aws-saa-c03"},
		{RawIndex: 2, Label: "az-900"},
	}
	filtered := filterOptions(options, "az-")
	if len(filtered) != 2 || filtered[0].RawIndex != 0 || filtered[1].RawIndex != 2 {
		t.Fatalf("filtered selection mapping changed: %+v", filtered)
	}
}

func TestWantsHelpRecognizesWindowsAndStandardForms(t *testing.T) {
	for _, arg := range []string{"/?", "-?", "-h", "--help", "-help", "help"} {
		if !wantsHelp([]string{arg}) {
			t.Errorf("expected %q to request help", arg)
		}
	}
	if wantsHelp([]string{"-debug"}) {
		t.Fatal("ordinary option was treated as help")
	}
}

func TestPrintUsageDocumentsOptionsAndInteractiveCommands(t *testing.T) {
	var output bytes.Buffer
	printUsage(&output)
	for _, expected := range []string{"Usage:", "-no-cache", "-refresh-index", "-recent-comments-days", "unknown dates remain", "/clear", "/reload", "/scan", "two-minute pass", "/all", "may be partial", "Enter one item at a time"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("help output does not contain %q", expected)
		}
	}
	for _, obsolete := range []string{"/text", "Download all provider discussions", "Keep questions with comments in the last N days"} {
		if strings.Contains(output.String(), obsolete) {
			t.Errorf("help output contains obsolete or misleading text %q", obsolete)
		}
	}
}

func TestSupportsANSIAllowsWindowsConsoleBeforeModeActivation(t *testing.T) {
	env := func(string) (string, bool) { return "", false }
	if !supportsANSI("windows", true, env) {
		t.Fatal("Windows console should attempt virtual-terminal colour activation")
	}
	noColor := func(key string) (string, bool) {
		if key == "NO_COLOR" {
			return "1", true
		}
		return "", false
	}
	if supportsANSI("windows", true, noColor) {
		t.Fatal("NO_COLOR should disable terminal colours")
	}
}
