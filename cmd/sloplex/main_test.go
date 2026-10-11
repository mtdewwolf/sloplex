package main

import (
	"strings"
	"testing"

	"github.com/mtdewwolf/sloplex/internal/gdm"
	"github.com/mtdewwolf/sloplex/internal/preferences"
)

func TestGDMNameFollowsPreference(t *testing.T) {
	prefs, err := preferences.Open("", preferences.Bootstrap{FriendlyName: "config-name"})
	if err != nil {
		t.Fatal(err)
	}
	info := gdmInfo(prefs, "abc", 32400, 1)
	if !strings.Contains(string(gdm.Response(info())), "Name: config-name\r\n") {
		t.Fatalf("response = %q", gdm.Response(info()))
	}
	if err := prefs.Set(map[string]string{preferences.FriendlyName: "Den"}); err != nil {
		t.Fatal(err)
	}
	for _, msg := range [][]byte{gdm.Response(info()), gdm.Hello(info())} {
		if !strings.Contains(string(msg), "Name: Den\r\n") {
			t.Errorf("message = %q", msg)
		}
	}
}
