package web

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/stretchr/testify/require"
)

func TestConfigSetWritesAnAllowedKey(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	c := dialStream(t, g, "")
	_ = readFrame(t, c)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/config/set",
		`{"key":"options.web_display.theme","value":"solid"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.JSONEq(t, `{"theme":"solid","motion":"","low_power":false,"cinematic_hint":true}`, string(body))
	require.Contains(t, f.CallLog(), `SetConfigField:global:options.web_display.theme:"solid"`)
	fr := nextFrameOfType(t.Context(), t, c, frameDisplayChanged)
	require.Contains(t, string(fr.Payload), `"theme":"solid"`)
}

func TestConfigSetRefusesOtherKeys(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	for _, key := range []string{
		"options.web", "options.agent_teams", "providers.openai.api_key",
		"options.web_display", "options.web_display.theme.extra", "options.tui.active_theme",
		"OPTIONS.WEB_DISPLAY.THEME", " options.web_display.theme",
		`options.web_display.theme\n`,
	} {
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/config/set",
			`{"key":"`+key+`","value":true}`)
		rsp.Body.Close()
		require.Equal(t, http.StatusBadRequest, rsp.StatusCode, key)
		require.Contains(t, string(body), "key_not_allowed", key)
	}
	require.Empty(t, f.CallLog())
}

func TestConfigSetValidatesValues(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	for _, body := range []string{
		`{"key":"options.web_display.theme","value":"neon"}`,
		`{"key":"options.web_display.theme","value":1}`,
		`{"key":"options.web_display.motion","value":""}`,
		`{"key":"options.web_display.low_power","value":"yes"}`,
		`{"key":"options.web_display.theme","value":null}`,
		`{"key":"options.web_display.low_power","value":null}`,
		`{"key":"options.web_display.theme","value":{}}`,
		`{"key":"options.web_display.theme"}`,
	} {
		rsp, rb := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/config/set", body)
		rsp.Body.Close()
		require.Equal(t, http.StatusBadRequest, rsp.StatusCode, body)
		require.Contains(t, string(rb), "bad_value", body)
	}
	require.Empty(t, f.CallLog())
}

func TestConfigRemoveRestoresTheDefault(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	c := authedClient(t, g)
	setRsp, _ := doJSON(t, c, http.MethodPost, g.URL()+"/api/config/set", `{"key":"options.web_display.cinematic_hint","value":false}`)
	setRsp.Body.Close()
	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/config/remove", `{"key":"options.web_display.cinematic_hint"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.Contains(t, string(body), `"cinematic_hint":true`)
	require.Contains(t, f.CallLog(), "RemoveConfigField:global:options.web_display.cinematic_hint")
}

func TestConfigWriteFailureKeepsTheSettings(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	stream := dialStream(t, g, "")
	_ = readFrame(t, stream)
	f.SetConfigErr(errors.New("disk full at /home/u/.local/share/crush/crush.json"))
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/config/set", `{"key":"options.web_display.theme","value":"solid"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusInternalServerError, rsp.StatusCode)
	require.NotContains(t, string(body), "/home/u")
	require.Equal(t, "holodeck", g.buildSnapshot(t.Context(), "").Display.Theme)
	// A refused write changes nothing, so no tab is told a new set arrived.
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	_, data, err := stream.Read(ctx)
	require.Error(t, err, "a failed write published %s", data)
}

func TestSnapshotCarriesDisplayFromConfig(t *testing.T) {
	t.Parallel()
	fake := webtest.NewFake()
	on := true
	fake.Cfg = &config.Config{Options: &config.Options{WebDisplay: &config.WebDisplayOptions{Motion: "off", LowPower: &on}}}
	g, err := Start(t.Context(), Options{Workspace: fake})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	d := g.buildSnapshot(t.Context(), "").Display
	require.Equal(t, DisplaySettings{Theme: "holodeck", Motion: "off", LowPower: true, CinematicHint: true}, d)
}

func TestConfigGetAnswersDisplayAndAgentTeams(t *testing.T) {
	t.Parallel()
	fake := webtest.NewFake()
	off := false
	fake.Cfg = &config.Config{Options: &config.Options{
		WebDisplay: &config.WebDisplayOptions{Theme: "solid"},
		AgentTeams: &off,
	}}
	g, err := Start(t.Context(), Options{Workspace: fake})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/config", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.JSONEq(t,
		`{"display":{"theme":"solid","motion":"","low_power":false,"cinematic_hint":true},"agent_teams":false}`,
		string(body))
}

func TestConfigRemoveAnUnsetKeySucceeds(t *testing.T) {
	t.Parallel()
	g, f := startTest(t) // the fake starts with no web_display block
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/config/remove",
		`{"key":"options.web_display.theme"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.Contains(t, string(body), `"theme":"holodeck"`)
	require.Contains(t, f.CallLog(), "RemoveConfigField:global:options.web_display.theme")
}

func TestConfigSetsAreSerialised(t *testing.T) {
	t.Parallel()
	gate := make(chan struct{})
	g, f := startTest(t)
	f.SetConfigGate(gate)
	c := authedClient(t, g)
	done := make(chan int, 2)
	set := func(body string) {
		rsp, _ := doJSON(t, c, http.MethodPost, g.URL()+"/api/config/set", body)
		rsp.Body.Close()
		done <- rsp.StatusCode
	}
	go set(`{"key":"options.web_display.theme","value":"solid"}`)
	require.Eventually(t, func() bool { return len(f.CallLog()) == 1 }, time.Second, 10*time.Millisecond)
	go set(`{"key":"options.web_display.motion","value":"off"}`)
	// The first write still holds the gate, so a second workspace call
	// would mean the two writes overlapped.
	time.Sleep(50 * time.Millisecond)
	require.Len(t, f.CallLog(), 1, "the second write waits for the first")
	close(gate)
	require.Equal(t, http.StatusOK, <-done)
	require.Equal(t, http.StatusOK, <-done)
	require.Len(t, f.CallLog(), 2)
}
