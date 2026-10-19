// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

func TestNormalizeResponsesInput(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"no mid developer", `{"model":"m","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`, ""},
		{"leading kept", `{"input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"sys"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`, ""},
		{"mid converted", `{"model":"m","input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"a"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},{"type":"message","role":"developer","content":[{"type":"input_text","text":"b"}]}]}`,
			`{"model":"m","input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"a"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},{"content":[{"text":"[developer]\nb","type":"input_text"}],"role":"user","type":"message"}]}`},
		{"unknown fields preserved", `{"z":1,"input":[{"type":"function_call","name":"f","x":[1,2]},{"type":"message","role":"system","content":[{"type":"input_text","text":"b"}]}],"a":{"k":2}}`,
			`{"z":1,"input":[{"type":"function_call","name":"f","x":[1,2]},{"content":[{"text":"[developer]\nb","type":"input_text"}],"role":"user","type":"message"}],"a":{"k":2}}`},
		{"string input", `{"input":"hello"}`, ""},
		{"invalid json", `{"input":[`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := c.want
			if want == "" {
				want = c.in
			}
			if got := string(normalizeResponsesInput([]byte(c.in))); got != want {
				t.Fatalf("got  %s\nwant %s", got, want)
			}
		})
	}
}
