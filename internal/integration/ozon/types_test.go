package ozon

import "testing"

func TestMicroRubToRub(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    int64
		wantErr bool
	}{
		{name: "zero", in: "0", want: 0},
		{name: "empty maps to zero", in: "", want: 0},
		{name: "one ruble", in: "1000000", want: 1},
		{name: "530 rubles", in: "530000000", want: 530},
		{name: "rounds half up", in: "1500000", want: 2},
		{name: "rounds down below half", in: "1499999", want: 1},
		{name: "whitespace tolerated", in: " 2000000 ", want: 2},
		{name: "garbage errors", in: "abc", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MicroRubToRub(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("MicroRubToRub(%q): expected error, got %d", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("MicroRubToRub(%q): unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("MicroRubToRub(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestMicroRubToRubFloat(t *testing.T) {
	got, err := MicroRubToRubFloat("1250000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 1.25 {
		t.Fatalf("MicroRubToRubFloat(1250000) = %v, want 1.25", got)
	}
}

func TestRubToMicroRub(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{1, "1000000"},
		{530, "530000000"},
	}
	for _, tc := range cases {
		if got := RubToMicroRub(tc.in); got != tc.want {
			t.Fatalf("RubToMicroRub(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Round trip through the parser.
	for _, rub := range []int64{0, 1, 37, 5000, 123456} {
		back, err := MicroRubToRub(RubToMicroRub(rub))
		if err != nil {
			t.Fatalf("round trip %d: unexpected error: %v", rub, err)
		}
		if back != rub {
			t.Fatalf("round trip %d = %d", rub, back)
		}
	}
}

func TestRubFloatToMicroRubRoundTrip(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{1.25, "1250000"},
		{37.5, "37500000"},
		{0.01, "10000"},
		{530, "530000000"},
	}
	for _, tc := range cases {
		got := RubFloatToMicroRub(tc.in)
		if got != tc.want {
			t.Fatalf("RubFloatToMicroRub(%v) = %q, want %q", tc.in, got, tc.want)
		}
		back, err := MicroRubToRubFloat(got)
		if err != nil {
			t.Fatalf("round trip %v: unexpected error: %v", tc.in, err)
		}
		if back != tc.in {
			t.Fatalf("round trip %v = %v", tc.in, back)
		}
	}
}

func TestParsePriceString(t *testing.T) {
	got, err := ParsePriceString("1990.0000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 1990 {
		t.Fatalf("ParsePriceString = %v, want 1990", got)
	}
	if _, err := ParsePriceString("not-a-price"); err == nil {
		t.Fatal("expected error for garbage price")
	}
}
