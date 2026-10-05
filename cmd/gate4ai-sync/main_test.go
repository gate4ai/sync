package main

import (
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		args    []string
		want    invocation
		wantErr bool
	}{
		{args: nil, want: invocation{}},
		{args: []string{"--host", "test.gate4.ai"}, want: invocation{host: "test.gate4.ai"}},
		{args: []string{"-psn_0_12345"}, want: invocation{}}, // macOS Finder launch
		{args: []string{"url"}, want: invocation{printURL: true}},
		// Each of these used to start a whole second client.
		{args: []string{"url", "extra"}, wantErr: true},
		{args: []string{"--host", "test.gate4.ai", "url"}, wantErr: true},
		{args: []string{"ulr"}, wantErr: true},
		{args: []string{"--nope"}, wantErr: true},
	}
	for _, c := range cases {
		got, err := parseArgs(c.args, io.Discard)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseArgs(%q) = %+v, want an error", c.args, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("parseArgs(%q) = %+v, %v; want %+v", c.args, got, err, c.want)
		}
	}
}

func TestParseArgsHelpMentionsURL(t *testing.T) {
	var out strings.Builder
	_, err := parseArgs([]string{"-h"}, &out)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("err = %v, want flag.ErrHelp", err)
	}
	if !strings.Contains(out.String(), "gate4ai-sync url") || !strings.Contains(out.String(), "-host") {
		t.Errorf("usage does not describe url and --host:\n%s", out.String())
	}
}

func TestSetupHint(t *testing.T) {
	hint := setupHint("http://127.0.0.1:23456/", "https://test.gate4.ai", true)
	for _, want := range []string{"Open: http://127.0.0.1:23456/", "sign in to test.gate4.ai.", "Tried to open it in your browser", "gate4ai-sync url"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint is missing %q:\n%s", want, hint)
		}
	}

	hint = setupHint("http://127.0.0.1:23456/", "https://gate4.ai", false)
	if strings.Contains(hint, "Tried to open") || !strings.Contains(hint, "Could not open a browser") {
		t.Errorf("hint claims a browser was opened when it was not:\n%s", hint)
	}
}
