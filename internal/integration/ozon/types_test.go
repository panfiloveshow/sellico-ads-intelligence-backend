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
