package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func float64Ptr(f float64) *float64 { return &f }
func intPtr(n int) *int             { return &n }
func boolPtr(b bool) *bool          { return &b }

func decodePrefs(t *testing.T, res *http.Response) Prefs {
	t.Helper()
	var p Prefs
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decode prefs from %q: %v", raw, err)
	}
	return p
}

func TestGetPrefsDefaults(t *testing.T) {
	f := newProgressFixture(t)
	res := f.call(t, f.srv.getPrefs, http.MethodGet, "/api/v1/me/prefs", 0, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	got := decodePrefs(t, res)
	want := Prefs{
		PlaybackRate:        1.0,
		SkipBackSeconds:     30,
		SkipForwardSeconds:  30,
		AutoRewind:          true,
		DefaultSleepMinutes: 0,
	}
	if got != want {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}
}

func TestPutPrefsPartialUpdate(t *testing.T) {
	f := newProgressFixture(t)

	res := f.call(t, f.srv.putPrefs, http.MethodPut, "/api/v1/me/prefs", 0,
		PrefsPatch{PlaybackRate: float64Ptr(1.5)})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("first put status = %d, want 200", res.StatusCode)
	}
	got := decodePrefs(t, res)
	if got.PlaybackRate != 1.5 {
		t.Fatalf("playback_rate = %v, want 1.5", got.PlaybackRate)
	}
	if got.SkipBackSeconds != 30 {
		t.Fatalf("skip_back_seconds = %v, want unchanged default 30", got.SkipBackSeconds)
	}

	// A second, unrelated patch must not clobber the first field.
	res = f.call(t, f.srv.putPrefs, http.MethodPut, "/api/v1/me/prefs", 0,
		PrefsPatch{SkipForwardSeconds: intPtr(15), AutoRewind: boolPtr(false), DefaultSleepMinutes: intPtr(20)})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("second put status = %d, want 200", res.StatusCode)
	}
	got2 := decodePrefs(t, res)
	want := Prefs{
		PlaybackRate:        1.5,
		SkipBackSeconds:     30,
		SkipForwardSeconds:  15,
		AutoRewind:          false,
		DefaultSleepMinutes: 20,
	}
	if got2 != want {
		t.Fatalf("second put = %+v, want %+v", got2, want)
	}

	res = f.call(t, f.srv.getPrefs, http.MethodGet, "/api/v1/me/prefs", 0, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want 200", res.StatusCode)
	}
	if got3 := decodePrefs(t, res); got3 != want {
		t.Fatalf("get after updates = %+v, want %+v", got3, want)
	}
}

func TestPutPrefsBadValues(t *testing.T) {
	f := newProgressFixture(t)

	cases := []struct {
		name string
		body PrefsPatch
	}{
		{"rate too low", PrefsPatch{PlaybackRate: float64Ptr(0.4)}},
		{"rate too high", PrefsPatch{PlaybackRate: float64Ptr(3.1)}},
		{"skip back too low", PrefsPatch{SkipBackSeconds: intPtr(4)}},
		{"skip back too high", PrefsPatch{SkipBackSeconds: intPtr(121)}},
		{"skip forward too low", PrefsPatch{SkipForwardSeconds: intPtr(4)}},
		{"skip forward too high", PrefsPatch{SkipForwardSeconds: intPtr(121)}},
		{"sleep negative", PrefsPatch{DefaultSleepMinutes: intPtr(-1)}},
		{"sleep too high", PrefsPatch{DefaultSleepMinutes: intPtr(181)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := f.call(t, f.srv.putPrefs, http.MethodPut, "/api/v1/me/prefs", 0, tc.body)
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", res.StatusCode)
			}
			if code := decodeProgressErr(t, res).Code; code != "bad_request" {
				t.Fatalf("error code = %q, want bad_request", code)
			}
		})
	}

	// Values are validated, never silently clamped: the failed patch above
	// must not have applied even its in-range fields (there were none here,
	// but a follow-up bad patch on top of a good one must not partially land).
	res := f.call(t, f.srv.putPrefs, http.MethodPut, "/api/v1/me/prefs", 0,
		PrefsPatch{PlaybackRate: float64Ptr(1.25), SkipBackSeconds: intPtr(9999)})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
	res = f.call(t, f.srv.getPrefs, http.MethodGet, "/api/v1/me/prefs", 0, nil)
	if got := decodePrefs(t, res); got.PlaybackRate != 1.0 {
		t.Fatalf("playback_rate after rejected patch = %v, want default 1.0 (nothing applied)", got.PlaybackRate)
	}
}

func TestPrefsRequireAuth(t *testing.T) {
	f := newProgressFixture(t)

	for _, tc := range []struct {
		method, path string
		h            http.HandlerFunc
	}{
		{http.MethodGet, "/api/v1/me/prefs", f.srv.getPrefs},
		{http.MethodPut, "/api/v1/me/prefs", f.srv.putPrefs},
	} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		user(tc.h).ServeHTTP(rec, r)
		res := rec.Result()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s %s without session: status = %d, want 401", tc.method, tc.path, res.StatusCode)
		}
	}
}
