package fetch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseAdvertisedQuestionCount(t *testing.T) {
	for _, test := range []struct {
		html string
		want int
	}{
		{html: `<a>Browse 286 Questions</a>`, want: 286},
		{html: `<span>browse 1,519 questions</span>`, want: 1519},
		{html: `<p>No count here</p>`, want: 0},
	} {
		if got := parseAdvertisedQuestionCount([]byte(test.html)); got != test.want {
			t.Errorf("parseAdvertisedQuestionCount(%q) = %d, want %d", test.html, got, test.want)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/exams/microsoft/az-305/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `<a>Browse 286 Questions</a>`)
	}))
	defer server.Close()
	oldBaseURL, oldClient := examTopicsBaseURL, client
	examTopicsBaseURL, client = server.URL, server.Client()
	defer func() { examTopicsBaseURL, client = oldBaseURL, oldClient }()

	got, err := GetOfficialExamQuestionCount(context.Background(), " Microsoft ", " AZ-305 ")
	if err != nil || got != 286 {
		t.Fatalf("GetOfficialExamQuestionCount() = (%d, %v), want (286, nil)", got, err)
	}
}
