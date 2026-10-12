package server

import (
	"encoding/xml"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestMediaProviderDiscovery(t *testing.T) {
	f := newFixture(t, Options{})
	mc := f.getJSON(t, "/media/providers")
	if mc.Size != 1 || len(mc.MediaProvider) != 1 || mc.MachineIdentifier != "abc123" {
		t.Fatalf("provider envelope: %+v", mc)
	}
	p := mc.MediaProvider[0]
	if p.Identifier != "com.plexapp.plugins.library" || p.Types != "video" || p.Protocols != "stream,download" {
		t.Fatalf("provider: %+v", p)
	}
	features := make(map[string]ProviderFeature)
	for _, feature := range p.Feature {
		if _, exists := features[feature.Type]; exists {
			t.Fatalf("duplicate feature %s", feature.Type)
		}
		features[feature.Type] = feature
	}
	if len(features) != 8 || features["playqueue"].Key != "/playQueues" || features["playlist"].Flavor != "universal" || features["timeline"].ScrobbleKey != "/:/scrobble" || features["timeline"].UnscrobbleKey != "/:/unscrobble" {
		t.Fatalf("features: %+v", features)
	}
	content := features["content"]
	if len(content.Directory) != 2 || content.Directory[1].Key != fmt.Sprintf("/library/sections/%d", f.section) || content.Directory[1].ID != fmt.Sprint(f.section) || len(content.Directory[1].Location) != 0 {
		t.Fatalf("content discovery: %+v", content)
	}
	// Follow the advertised section and queue keys through real handlers.
	section := f.getJSON(t, content.Directory[1].Key+"/all")
	var queue containerResp
	if resp := f.api(t, "POST", features["playqueue"].Key+"?type=video&uri="+url.QueryEscape(section.Metadata[0].Key), testToken, nil, &queue); resp.StatusCode != 200 || queue.MediaContainer.Size != 1 {
		t.Fatalf("discovered queue: status %d, %+v", resp.StatusCode, queue)
	}
	_, _, body := f.raw(t, "/media/providers?ignore_opt_outs=true", testToken)
	var decoded MediaContainer
	if err := xml.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Size != 1 || len(decoded.MediaProvider) != 1 || len(decoded.MediaProvider[0].Feature) != 8 || !strings.Contains(string(body), `scrobbleKey="/:/scrobble"`) {
		t.Fatalf("XML provider: %s", body)
	}
	root := f.getJSON(t, "/")
	if len(root.MediaProvider) != 0 {
		t.Fatal("ordinary container acquired provider entries")
	}
}

func TestMediaProviderVisibility(t *testing.T) {
	f := newFixture(t, Options{})
	if resp := f.api(t, "GET", "/media/providers", "", nil, nil); resp.StatusCode != 401 {
		t.Fatalf("unauthenticated discovery: %d", resp.StatusCode)
	}
	bob := f.createUser(t, "bob", "builder99", false)
	token := f.login(t, "bob", "builder99")
	var response containerResp
	f.api(t, "GET", "/media/providers", token, nil, &response)
	if len(response.MediaContainer.MediaProvider[0].Feature[0].Directory) != 2 {
		t.Fatal("visible section absent")
	}
	f.api(t, "PATCH", fmt.Sprintf("/sloplex/accounts/%d", bob.ID), testToken, map[string]any{"libraries": []int{}}, nil)
	response = containerResp{}
	f.api(t, "GET", "/media/providers", token, nil, &response)
	if len(response.MediaContainer.MediaProvider[0].Feature[0].Directory) != 1 {
		t.Fatalf("inaccessible section exposed: %+v", response)
	}
}
