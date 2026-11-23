package sitebuilder

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// pngBytes starts like a PNG, which is all the worker checks.
var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)

type fakeIllustrator struct {
	calls   []string
	fail    map[string]error
	data    []byte
	mediaOf string
}

func (f *fakeIllustrator) Illustrate(_ context.Context, _ uint, prompt, aspect string) ([]byte, string, string, error) {
	f.calls = append(f.calls, prompt)
	for name, err := range f.fail {
		if strings.Contains(prompt, name) {
			return nil, "", "", err
		}
	}
	media := f.mediaOf
	if media == "" {
		media = "image/png"
	}
	return f.data, media, "fake-image-model", nil
}

func testDirection(t *testing.T) Direction {
	t.Helper()
	for _, d := range Directions() {
		return d
	}
	t.Fatal("no directions")
	return Direction{}
}

func TestProductsAreTrimmedAndCapped(t *testing.T) {
	var products []BriefProduct
	for _, name := range []string{" Kopi Susu ", "kopi susu", "", "Roti", "Teh", "Es Jeruk", "Donat", "Bakso", "Mie"} {
		products = append(products, BriefProduct{Name: name})
	}
	kept := normalizeProducts(products)
	if len(kept) != MaxIllustrations {
		t.Fatalf("kept %d products, want %d", len(kept), MaxIllustrations)
	}
	if kept[0].Name != "Kopi Susu" || kept[1].Name != "Roti" {
		t.Fatalf("names not trimmed or duplicates kept: %+v", kept[:2])
	}
}

func TestIllustrationsAreDrawnOnceAndOnlyForProducts(t *testing.T) {
	dir := t.TempDir()
	direction := testDirection(t)
	brief := &Brief{Name: "Kedai", Language: "id", Products: []BriefProduct{{Name: "Kopi Susu", Text: "kopi dengan susu segar"}, {Name: "Roti Bakar"}, {Name: "Teh Tarik"}}}
	fake := &fakeIllustrator{data: pngBytes, fail: map[string]error{"Teh Tarik": errors.New("busy")}}
	// The owner's own photo of the bread comes first: it is not drawn.
	drawing := Drawing{Illustrator: fake, FilesDir: dir, Direction: direction, Images: []Image{{Src: "/uploads/a.jpg", Alt: "Roti bakar coklat"}}}

	detail := drawing.Draw(context.Background(), brief)
	if len(fake.calls) != 2 {
		t.Fatalf("drew %d pictures, want 2 (coffee and the failing tea): %v", len(fake.calls), detail)
	}
	coffee := brief.Products[0]
	if !strings.HasPrefix(coffee.Image, "/uploads/produk-kopi-susu-") || !strings.HasSuffix(coffee.Image, ".png") || coffee.Alt != "Ilustrasi Kopi Susu" {
		t.Fatalf("coffee not recorded: %+v", coffee)
	}
	if ReadSiteFile(dir, "web/public"+coffee.Image) == "" {
		t.Fatal("the picture was not written into the site")
	}
	if brief.Products[1].Image != "" || brief.Products[2].Image != "" {
		t.Fatalf("bread (own photo) or tea (failed) got a picture: %+v", brief.Products)
	}
	for _, prompt := range fake.calls {
		if !strings.Contains(prompt, "not a photograph") || !strings.Contains(prompt, "no people") {
			t.Fatalf("prompt does not keep to drawings without people: %s", prompt)
		}
	}

	// A second run draws only what is still missing.
	fake.calls, fake.fail = nil, nil
	drawing.Draw(context.Background(), brief)
	if len(fake.calls) != 1 || !strings.Contains(fake.calls[0], "Teh Tarik") {
		t.Fatalf("second run drew %v, want only the tea", fake.calls)
	}

	// Another colour of the look draws them again.
	fake.calls = nil
	brief.Colours = map[string]string{"primary": "#0E7490"}
	drawing.Draw(context.Background(), brief)
	if len(fake.calls) != 2 {
		t.Fatalf("new colours drew %d pictures, want 2", len(fake.calls))
	}
}

func TestUnusableOrMissingPicturesLeaveProductsPlain(t *testing.T) {
	brief := &Brief{Name: "Toko", Products: []BriefProduct{{Name: "Tas"}, {Name: "Dompet"}}}
	drawing := Drawing{Illustrator: &fakeIllustrator{data: []byte("<svg/>"), mediaOf: "image/svg+xml"}, FilesDir: t.TempDir(), Direction: testDirection(t)}
	drawing.Draw(context.Background(), brief)
	for _, product := range brief.Products {
		if product.Image != "" {
			t.Fatalf("an unusable picture was kept: %+v", product)
		}
	}
	stop := &fakeIllustrator{fail: map[string]error{"": ErrNoIllustrator}}
	drawing.Illustrator = stop
	drawing.Draw(context.Background(), brief)
	if len(stop.calls) != 1 {
		t.Fatalf("kept asking after no image model was set up: %d calls", len(stop.calls))
	}
}

func TestOnlyTheWorkersPicturesReachTheSite(t *testing.T) {
	brief := Brief{Name: "Toko", Language: "id", Pages: []BriefPage{{Title: "Beranda"}}, Products: []BriefProduct{
		{Name: "Tas", Image: "/uploads/produk-tas-1234abcd.png", Alt: "Ilustrasi Tas"},
		{Name: "Dompet", Image: "https://example.com/dompet.png", Alt: "x"},
		{Name: "Sabuk", Image: "/uploads/produk-../../secret.png"},
	}}
	ts := SiteTS(brief, nil)
	if !strings.Contains(ts, `"/uploads/produk-tas-1234abcd.png"`) || !strings.Contains(ts, "export function productImage") {
		t.Fatalf("site.ts misses the illustration or the helper:\n%s", ts)
	}
	if strings.Contains(ts, "example.com") || strings.Contains(ts, "secret") {
		t.Fatalf("site.ts carries a picture the worker did not draw:\n%s", ts)
	}
	planned := Brief{Products: []BriefProduct{{Name: "Tas", Image: "/uploads/produk-x.png", Alt: "a", Key: "k"}}}
	planned.ForgetIllustrations()
	if planned.Products[0].Image != "" || planned.Products[0].Key != "" {
		t.Fatal("a planned brief kept a picture path from the model")
	}
}
