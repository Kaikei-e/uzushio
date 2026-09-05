package routes

import "testing"

func TestLookupIgnoresMethodCase(t *testing.T) {
	tab := New()
	tab.Add("GET", "/orders", "listOrders")
	tab.Add("post", "/orders", "createOrder")

	cases := []struct {
		method, path, want string
	}{
		{"GET", "/orders", "listOrders"},
		{"get", "/orders", "listOrders"},
		{"Get", "/orders", "listOrders"},
		{"POST", "/orders", "createOrder"},
		{"post", "/orders", "createOrder"},
	}
	for _, c := range cases {
		got, ok := tab.Lookup(c.method, c.path)
		if !ok || got != c.want {
			t.Errorf("Lookup(%q, %q) = (%q, %v), want (%q, true)", c.method, c.path, got, ok, c.want)
		}
	}
}

func TestLookupKeepsPathCase(t *testing.T) {
	tab := New()
	tab.Add("GET", "/Orders", "listOrders")

	if _, ok := tab.Lookup("GET", "/orders"); ok {
		t.Error(`Lookup("GET", "/orders") found a route registered for "/Orders"`)
	}
	if _, ok := tab.Lookup("GET", "/Orders"); !ok {
		t.Error(`Lookup("GET", "/Orders") = not found, want found`)
	}
	if _, ok := tab.Lookup("DELETE", "/Orders"); ok {
		t.Error(`Lookup("DELETE", "/Orders") found a route that was never added`)
	}
}
