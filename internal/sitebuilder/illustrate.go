package sitebuilder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentic-ai-backend/models"
)

// Product illustrations (WEBSITE-BUILDER.md, section 4, "Product illustrations"): an
// image model draws one illustration for each product, dish or item the person named,
// so a list of products has a picture without inventing photos. Only products: never
// people, places, the hero or the gallery, which stay the owner's own photos.

// MaxIllustrations is how many products of one site get an illustration.
const MaxIllustrations = 6

// illustrationTimeout bounds one image call; a run never waits long for a picture.
const illustrationTimeout = 2 * time.Minute

// maxIllustrationBytes refuses an answer larger than an uploaded photo may be.
const maxIllustrationBytes = 3 << 20

// ErrNoIllustrator means no image model is set up: products are shown without pictures.
var ErrNoIllustrator = errors.New("no image model is set up")

// Illustrator draws one picture from a prompt. The agent worker writes the picture into
// the site's own folder, so the site itself never calls an outside service.
type Illustrator interface {
	Illustrate(ctx context.Context, userID uint, prompt, aspect string) (data []byte, mediaType, model string, err error)
}

// BriefProduct is a product, dish or item the person named. The planner writes Name and
// Text from the person's words; the worker fills Image, Alt and Key.
type BriefProduct struct {
	Name string `json:"name"`
	Text string `json:"text,omitempty"`
	// Image is the illustration's path on the site (/uploads/...), "" until one is made.
	Image string `json:"image,omitempty"`
	Alt   string `json:"alt,omitempty"`
	// Key says what the illustration was drawn from; another product text or look draws it again.
	Key string `json:"key,omitempty"`
}

// normalizeProducts trims the planner's products: named, unique, at most MaxIllustrations.
func normalizeProducts(products []BriefProduct) []BriefProduct {
	seen := map[string]bool{}
	kept := make([]BriefProduct, 0, len(products))
	for _, product := range products {
		product.Name = clipRunes(strings.TrimSpace(product.Name), 60)
		product.Text = clipRunes(strings.TrimSpace(product.Text), 200)
		key := strings.ToLower(product.Name)
		if product.Name == "" || seen[key] {
			continue
		}
		seen[key] = true
		kept = append(kept, product)
		if len(kept) == MaxIllustrations {
			break
		}
	}
	return kept
}

// illustrationPrefix starts the path of every illustration the worker writes.
const illustrationPrefix = "/uploads/produk-"

// ForgetIllustrations clears every picture of the products, for a brief that came from a
// model: only the worker draws illustrations and names their files.
func (b *Brief) ForgetIllustrations() {
	for index := range b.Products {
		b.Products[index].Image, b.Products[index].Alt, b.Products[index].Key = "", "", ""
	}
}

// ownIllustration is true for a path the worker wrote itself.
func ownIllustration(src string) bool {
	return strings.HasPrefix(src, illustrationPrefix) && !strings.Contains(src, "..") && !strings.ContainsAny(src, "?#\\")
}

func clipRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit]))
}

// IllustrationKey identifies what an illustration shows and in which colours.
func IllustrationKey(p BriefProduct, palette map[string]string) string {
	sum := sha256.New()
	fmt.Fprintf(sum, "%s\n%s\n", strings.ToLower(p.Name), p.Text)
	for _, key := range PaletteKeys {
		fmt.Fprintf(sum, "%s=%s\n", key, palette[key])
	}
	return hex.EncodeToString(sum.Sum(nil))[:16]
}

// IllustrationPrompt asks for a drawing of one product in the look's colours. It asks for
// an illustration, not a photograph, so a visitor never takes it for the real product,
// and for no people, text or logos, which an image model would invent.
func IllustrationPrompt(p BriefProduct, d Direction, palette map[string]string) string {
	subject := p.Name
	if p.Text != "" {
		subject += ": " + p.Text
	}
	moods := strings.Join(d.Moods, ", ")
	return fmt.Sprintf(`A clean flat illustration of %s.
One product only, centred, seen whole, on a plain background of %s.
Colours: mainly %s and %s, with %s as a small accent. Mood: %s.
Style: a simple modern illustration with soft shapes, not a photograph.
No text, no letters, no numbers, no logos, no brand names, no people, no hands, no faces.`,
		subject, palette["surface"], palette["primary"], palette["foreground"], palette["accent"], moods)
}

// ownPhotoFor is true when the owner uploaded a photo whose description names the product:
// their own photo comes first, so no illustration is drawn for it.
func ownPhotoFor(p BriefProduct, images []Image) bool {
	name := strings.ToLower(p.Name)
	for _, image := range images {
		if !image.Logo && name != "" && strings.Contains(strings.ToLower(image.Alt), name) {
			return true
		}
	}
	return false
}

// IllustrationAlt is the picture's description for visitors who cannot see it.
func IllustrationAlt(p BriefProduct, language string) string {
	if language == "en" {
		return "Illustration of " + p.Name
	}
	return "Ilustrasi " + p.Name
}

// illustrate draws the products that have no current illustration. It never fails the
// run: a product without a picture is shown without one.
func (j *job) illustrate() {
	if j.w.Illustrator == nil {
		return
	}
	drawing := Drawing{Illustrator: j.w.Illustrator, UserID: j.site.UserID, FilesDir: j.filesDir, Direction: j.direction, Variant: j.variant, Images: j.images}
	if len(drawing.Needed(&j.brief)) == 0 {
		return
	}
	step := j.step(models.StepModel, say(j.lang, "illustrate"))
	detail := drawing.Draw(j.ctx, &j.brief)
	// Finished either way: without pictures the website is made all the same.
	j.done(step, models.StepDone, strings.Join(detail, "\n"))
	j.keep()
}

// Drawing is what drawing the illustrations of one site needs.
type Drawing struct {
	Illustrator Illustrator
	UserID      uint
	FilesDir    string
	Direction   Direction
	Variant     string
	Images      []Image
}

func (d Drawing) palette(b *Brief) map[string]string {
	return ThemePalette(d.Direction, d.Variant, b.Colours)
}

// Needed lists the products still to draw. A product the owner has their own photo of
// loses its illustration.
func (d Drawing) Needed(b *Brief) []int {
	palette := d.palette(b)
	var todo []int
	for index, product := range b.Products {
		if ownPhotoFor(product, d.Images) {
			b.Products[index].Image, b.Products[index].Alt, b.Products[index].Key = "", "", ""
			continue
		}
		if ownIllustration(product.Image) && product.Key == IllustrationKey(product, palette) && ReadSiteFile(d.FilesDir, "web/public"+product.Image) != "" {
			continue
		}
		todo = append(todo, index)
	}
	return todo
}

// Draw draws the needed products into the site's uploads and records them in the brief.
// It returns what happened, for the run log.
func (d Drawing) Draw(parent context.Context, b *Brief) []string {
	palette := d.palette(b)
	var detail []string
	made := 0
	for _, index := range d.Needed(b) {
		if parent.Err() != nil {
			break
		}
		product := b.Products[index]
		ctx, cancel := context.WithTimeout(parent, illustrationTimeout)
		data, mediaType, model, err := d.Illustrator.Illustrate(ctx, d.UserID, IllustrationPrompt(product, d.Direction, palette), "4:3")
		cancel()
		if errors.Is(err, ErrNoIllustrator) || errors.Is(err, ErrTokenLimit) {
			detail = append(detail, err.Error())
			break
		}
		if err != nil {
			detail = append(detail, product.Name+": "+err.Error())
			continue
		}
		extension := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp"}[mediaType]
		if extension == "" || len(data) == 0 || len(data) > maxIllustrationBytes {
			detail = append(detail, fmt.Sprintf("%s: unusable picture (%s, %d bytes)", product.Name, mediaType, len(data)))
			continue
		}
		key := IllustrationKey(product, palette)
		src := illustrationPrefix + PageSlug(product.Name) + "-" + key[:8] + extension
		if err := WriteSiteFile(d.FilesDir, "web/public"+src, string(data)); err != nil {
			detail = append(detail, product.Name+": "+err.Error())
			continue
		}
		b.Products[index].Image, b.Products[index].Alt, b.Products[index].Key = src, IllustrationAlt(product, b.Language), key
		detail = append(detail, fmt.Sprintf("%s: %s (%s)", product.Name, src, model))
		made++
	}
	if made == 0 {
		detail = append(detail, "no illustration was made; the products are shown without pictures")
	}
	return detail
}
