package perm

import "testing"

func TestGrantAndHas(t *testing.T) {
	for n := 0; n < 8; n++ {
		m := Grant(0, n)
		if want := uint8(1) << n; m != want {
			t.Errorf("Grant(0, %d) = %08b, want %08b", n, m, want)
		}
		if !Has(m, n) {
			t.Errorf("Has(Grant(0, %d), %d) = false, want true", n, n)
		}
		if Has(0, n) {
			t.Errorf("Has(0, %d) = true, want false", n)
		}
		if !Has(0xFF, n) {
			t.Errorf("Has(0xFF, %d) = false, want true", n)
		}
	}
}

func TestOnlyTheGrantedBit(t *testing.T) {
	m := Grant(Grant(0, 0), 3)
	if m != 0b00001001 {
		t.Errorf("granting 0 and 3 gives %08b, want %08b", m, 0b00001001)
	}
	for _, n := range []int{1, 2, 4, 5, 6, 7} {
		if Has(m, n) {
			t.Errorf("Has(%08b, %d) = true, want false", m, n)
		}
	}
	if Grant(m, 3) != m {
		t.Error("granting a permission twice changed the mask")
	}
}
