package charts

import "testing"

func TestStandardApp_Loads(t *testing.T) {
	c, err := StandardApp()
	if err != nil {
		t.Fatal(err)
	}
	if c.Name() != "standard-app" || len(c.Templates) == 0 {
		t.Fatalf("chart incomplet : %s, %d templates", c.Name(), len(c.Templates))
	}
}
