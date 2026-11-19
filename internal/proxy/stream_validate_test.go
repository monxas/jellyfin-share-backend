package proxy

import "testing"

func TestValidStreamPath(t *testing.T) {
	ok := []string{"master.m3u8", "main.m3u8", "hls1/main/0.ts", "hls1/main/12.m4s", "stream", "init.mp4"}
	bad := []string{"", "../../Users", "../../Users?x=.m3u8", "a/../../b.m3u8", "/abs.m3u8", "a//b.ts",
		"%2e%2e/Users.m3u8", "x.m3u8?y", "Users", "a/b.ts/", "..", "a\\b.ts"}
	for _, p := range ok {
		if !validStreamPath(p) {
			t.Errorf("rejected valid path %q", p)
		}
	}
	for _, p := range bad {
		if validStreamPath(p) {
			t.Errorf("accepted bad path %q", p)
		}
	}
}

func TestValidItemID(t *testing.T) {
	ok := []string{"0123456789abcdef0123456789ABCDEF", "01234567-89ab-cdef-0123-456789abcdef"}
	bad := []string{"", "../../Users", "0123456789abcdef0123456789abcde", "0123456789abcdef0123456789abcdef/x", "g123456789abcdef0123456789abcdef"}
	for _, id := range ok {
		if !validItemID(id) {
			t.Errorf("rejected valid id %q", id)
		}
	}
	for _, id := range bad {
		if validItemID(id) {
			t.Errorf("accepted bad id %q", id)
		}
	}
}
