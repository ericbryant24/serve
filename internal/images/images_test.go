package images

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func pngBytes(t *testing.T, w int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, 1))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestSaveAndRefs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	data := pngBytes(t, 3)
	name, err := Save(data)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(name) != ".png" {
		t.Fatalf("name %q: want a .png", name)
	}
	again, err := Save(data)
	if err != nil || again != name {
		t.Fatalf("saving the same bytes again gave %q, %v; want %q", again, err, name)
	}
	p, ok := Path(name)
	if !ok {
		t.Fatalf("Path(%q) refused a name Save made", name)
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, data) {
		t.Fatal("the stored file differs from what was saved")
	}
	other, _ := Save(pngBytes(t, 4))
	text := "Look ![a](" + URLPrefix + name + ") and ![b](" + URLPrefix + other + ") and ![a again](" + URLPrefix + name + ")"
	refs := Refs(text)
	if len(refs) != 2 || refs[0] != p || filepath.Base(refs[1]) != other {
		t.Fatalf("Refs = %v", refs)
	}
}

func TestSaveRefusesOtherTypes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, data := range [][]byte{
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		[]byte("just text"),
	} {
		if _, err := Save(data); err != ErrType {
			t.Errorf("Save(%.20q) = %v, want ErrType", data, err)
		}
	}
}

func TestPathRefusesOtherNames(t *testing.T) {
	for _, name := range []string{"../daemon.json", "abc.png", "0123456789abcdef0123456789abcdef.svg", "0123456789abcdef0123456789abcdef.png/x"} {
		if _, ok := Path(name); ok {
			t.Errorf("Path(%q) accepted it", name)
		}
	}
}
