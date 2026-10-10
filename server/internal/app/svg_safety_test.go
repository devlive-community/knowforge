package app

import "testing"

func TestCheckSVG(t *testing.T) {
	safe := []string{
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M0 0h24v24H0z" fill="#333"/></svg>`,
		`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><defs><path id="a" d="M0 0"/></defs><use xlink:href="#a"/><a href="https://example.com"><text>x</text></a></svg>`,
	}
	for _, s := range safe {
		if err := checkSVG([]byte(s)); err != nil {
			t.Errorf("safe SVG rejected: %v\n%s", err, s)
		}
	}
	unsafe := []string{
		`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><a href="javascript:alert(1)"><text>x</text></a></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><use xlink:href=" JavaScript:alert(1)"/></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><foreignObject><div/></foreignObject></svg>`,
		`<!DOCTYPE svg [<!ENTITY x "y">]><svg xmlns="http://www.w3.org/2000/svg">&x;</svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><rect style="background:url(javascript:alert(1))"/></svg>`,
	}
	for _, s := range unsafe {
		if err := checkSVG([]byte(s)); err != errUnsafeSVG {
			t.Errorf("unsafe SVG accepted (err=%v):\n%s", err, s)
		}
	}
	for _, s := range []string{`<html><body/></html>`, `not xml at all`, ``} {
		if err := checkSVG([]byte(s)); err == nil {
			t.Errorf("non-SVG accepted: %q", s)
		}
	}
}
