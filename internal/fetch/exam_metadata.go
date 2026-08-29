package fetch

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var advertisedQuestionCountPattern = regexp.MustCompile(`(?i)Browse\s+([0-9][0-9,]*)\s+Questions?`)

// GetOfficialExamQuestionCount reads the question count advertised on the
// public exam landing page. It is coverage metadata, not a guarantee that all
// question bodies are anonymously accessible through the official viewer.
func GetOfficialExamQuestionCount(ctx context.Context, provider, exam string) (int, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	exam = strings.ToLower(strings.TrimSpace(exam))
	if provider == "" || exam == "" {
		return 0, fmt.Errorf("provider and exam are required")
	}
	url := fmt.Sprintf("%s/exams/%s/%s/", examTopicsBaseURL, provider, exam)
	body, _, err := FetchURLContext(ctx, url, *client, MetadataRequestPolicy)
	if err != nil {
		return 0, err
	}
	count := parseAdvertisedQuestionCount(body)
	if count == 0 {
		return 0, fmt.Errorf("advertised question count was not found")
	}
	return count, nil
}

func parseAdvertisedQuestionCount(body []byte) int {
	match := advertisedQuestionCountPattern.FindSubmatch(body)
	if len(match) != 2 {
		return 0
	}
	count, err := strconv.Atoi(strings.ReplaceAll(string(match[1]), ",", ""))
	if err != nil || count < 1 {
		return 0
	}
	return count
}
