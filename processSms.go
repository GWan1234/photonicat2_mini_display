package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io/ioutil"
	"os"
	"strings"
	"time"
	"unicode"
	"sort"
	"os/exec"
	"log"

	"github.com/golang/freetype"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

type SMS struct {
	Index     int    `json:"index"`
	Sender    string `json:"sender"`
	Timestamp string `json:"timestamp"`
	Reference int    `json:"reference,omitempty"`
	Part      int    `json:"part,omitempty"`
	Total     int    `json:"total,omitempty"`
	Content   string `json:"content"`
}

func collectAndDrawSms(cfg *Config) int {
    jsonContent := getJsonContent(cfg)
    rawImgs, err := drawSmsFrJson(jsonContent, true)
    if err != nil {
        log.Println("Error drawing SMS:", err)
        return 0
    }

    // prepare the global slice
    smsPagesImages = make([]*image.RGBA, len(rawImgs))
    for i, img := range rawImgs {
        // try a direct cast
        rgba, ok := img.(*image.RGBA)
        if !ok {
            log.Printf("Image %d is not *image.RGBA, converting…", i)
            // convert by drawing into a new RGBA
            b := img.Bounds()
            r := image.NewRGBA(b)
            draw.Draw(r, b, img, b.Min, draw.Src)
            rgba = r
        }
        smsPagesImages[i] = rgba
    }

    return len(smsPagesImages)
}


func getJsonContent(cfg *Config) string {
    // exec.Command takes the program name, then each arg as its own string
    cmd := exec.Command("/usr/bin/sms_tool", "-j", "-d", cfg.ModemPort, "recv")
	log.Println(cmd)
    // it’s often helpful to capture stderr too
    output, err := cmd.CombinedOutput()
    if err != nil {
        log.Printf("sms_tool failed: %v\noutput: %s", err, output)
        return ""
    }
    return strings.TrimSpace(string(output))
}


func drawSmsFrJson(jsonContent string, savePng bool) (imgs []image.Image, err error) {
	var smsData struct {
		Msg []SMS `json:"msg"`
	}
	if err := json.Unmarshal([]byte(jsonContent), &smsData); err != nil {
		log.Printf("Error parsing JSON: %v\n", err)
		return nil, err
	}

	// Load font
	fontPath := assetsPrefix + "/assets/fonts/NotoSansMonoCJK-VF.ttf.ttc"
	log.Println("sms using font:", fontPath)
	fontBytes, err := ioutil.ReadFile(fontPath)
	if err != nil {
		fmt.Printf("Error loading font: %v\n", err)
		return
	}
	fnt, err := truetype.Parse(fontBytes)
	if err != nil {
		fmt.Printf("Error parsing font: %v\n", err)
		return
	}

	// Setup constants
	width, height := 172, 270
	fontSize := 12.0
	fontSizeTitle := 11.0
	lineSpacing := 1.2
	maxWidth := width - 8 // Adjusted for padding
	maxHeight := height - 8 // Adjusted for padding
	topPadding := 3.0
	xStart := 4
	layout := "01/02/06 15:04:05"

	// Create font context
	fc := freetype.NewContext()
	fc.SetDPI(72)
	fc.SetFont(fnt)
	fc.SetFontSize(fontSize)
	fc.SetSrc(image.NewUniform(color.RGBA{255, 255, 0, 255}))
	fc.SetHinting(font.HintingFull)

	fcTitle := freetype.NewContext()
	fcTitle.SetDPI(72)
	fcTitle.SetFont(fnt)
	fcTitle.SetFontSize(fontSizeTitle)
	fcTitle.SetSrc(image.NewUniform(color.RGBA{255, 255, 255, 255}))
	fcTitle.SetHinting(font.HintingFull)

	// Combine multipart messages
	type MsgKey struct {
		Sender    string
		Timestamp string
		Reference int
	}

	grouped := map[MsgKey][]SMS{}
	singles := []SMS{}

	for _, msg := range smsData.Msg {
		if msg.Total > 1 {
			key := MsgKey{msg.Sender, msg.Timestamp, msg.Reference}
			grouped[key] = append(grouped[key], msg)
		} else {
			singles = append(singles, msg)
		}
	}

	var mergedMsgs []SMS
	for key, parts := range grouped {
		sort.Slice(parts, func(i, j int) bool {
			return parts[i].Part < parts[j].Part
		})
		full := ""
		for _, p := range parts {
			full += p.Content
		}
		mergedMsgs = append(mergedMsgs, SMS{
			Sender:    key.Sender,
			Timestamp: key.Timestamp,
			Content:   full,
		})
	}

	smsData.Msg = append(singles, mergedMsgs...)

	// Sort messages by timestamp descending
	sort.Slice(smsData.Msg, func(i, j int) bool {
		ti, err1 := time.Parse(layout, smsData.Msg[i].Timestamp)
		tj, err2 := time.Parse(layout, smsData.Msg[j].Timestamp)
		if err1 != nil || err2 != nil {
			return smsData.Msg[i].Timestamp > smsData.Msg[j].Timestamp
		}
		return ti.After(tj)
	})

	// Prepare pagination
	type Line struct {
		Text      string
		IsTitle   bool
	}

	type Page struct {
		Lines []Line
	}

	var pages []Page
	currentPage := Page{}
	currentHeight := topPadding

	for _, msg := range smsData.Msg {
		timestamp, _ := time.Parse(layout, msg.Timestamp)
		formattedTime := timestamp.Format("2006-01-02 15:04")
		title := fmt.Sprintf("%s|%s", msg.Sender, formattedTime)
		message := msg.Content

		faceMeasure := truetype.NewFace(fnt, &truetype.Options{
			Size:    fontSize,
			DPI:     72,
			Hinting: font.HintingFull,
		})
		lines := wrapText(message, maxWidth, faceMeasure)

		// Add title
		titleHeight := fontSizeTitle * lineSpacing
		if currentHeight+titleHeight > float64(maxHeight) {
			// Save current page
			pages = append(pages, currentPage)
			// Reset for new page
			currentPage = Page{}
			currentHeight = topPadding
		}
		currentPage.Lines = append(currentPage.Lines, Line{Text: title, IsTitle: true})
		currentHeight += titleHeight

		// Add message lines
		for _, line := range lines {
			lineHeight := fontSize * lineSpacing
			if currentHeight+lineHeight > float64(maxHeight) {
				// Save current page
				pages = append(pages, currentPage)
				// Reset for new page
				currentPage = Page{}
				currentHeight = topPadding
				// Repeat title on new page
				currentPage.Lines = append(currentPage.Lines, Line{Text: title, IsTitle: true})
				currentHeight += titleHeight
			}
			currentPage.Lines = append(currentPage.Lines, Line{Text: line, IsTitle: false})
			currentHeight += lineHeight
		}
		// Add spacing after message
		spacingHeight := fontSize * lineSpacing
		if currentHeight+spacingHeight <= float64(maxHeight) {
			currentPage.Lines = append(currentPage.Lines, Line{Text: "", IsTitle: false})
			currentHeight += spacingHeight
		}
	}

	// Add the last page if it has content
	if len(currentPage.Lines) > 0 {
		pages = append(pages, currentPage)
	}

	// Render pages to PNG
	for i, page := range pages {
		fmt.Printf("Rendering page %d\n", i)
		img := image.NewRGBA(image.Rect(0, 0, width, height))
		draw.Draw(img, img.Bounds(), &image.Uniform{color.Black}, image.Point{}, draw.Src)

		fc.SetDst(img)
		fc.SetClip(img.Bounds())
		fcTitle.SetDst(img)
		fcTitle.SetClip(img.Bounds())

		y := topPadding
		for _, line := range page.Lines {
			if line.Text == "" {
				y += fontSize * lineSpacing
				continue
			}
			if line.IsTitle {
				timePrefix := ""
				lineTitle := strings.Split(line.Text, "|")
				sender := lineTitle[0]
				dateStr := strings.Split(lineTitle[1], " ")[0]
				timeStr := strings.Split(lineTitle[1], " ")[1]
				today := time.Now()
				yesterday := today.AddDate(0, 0, -1)

				if dateStr == today.Format("2006-01-02") {
					timePrefix = "Today"
				} else if dateStr == yesterday.Format("2006-01-02") {
					timePrefix = "Y-day"
				} else {
					// Parse the date string
					t, err := time.Parse("2006-01-02", dateStr)
					if err == nil {
						if t.Year() == today.Year() {
							timePrefix = t.Format("01-02") // MM DD
						} else {
							timePrefix = t.Format("2006-01-02")
						}
					} else {
						// fallback: just use the original string
						//timePrefix = dateStr
					}
				}
				timeDisplay := timePrefix + " " + timeStr
				if len(sender) > 15 {
					sender = sender[:8] + "**" + sender[len(sender)- 2:]
				}
				// Draw sender left-aligned
				ptSender := freetype.Pt(xStart, int(y+fontSizeTitle))
				_, err := fcTitle.DrawString(sender, ptSender)
				if err != nil {
					fmt.Printf("Error drawing sender: %v\n", err)
					return nil, err
				}
				// Draw timeDisplay right-aligned
				faceTitle := truetype.NewFace(fnt, &truetype.Options{
					Size:    fontSizeTitle,
					DPI:     72,
					Hinting: font.HintingFull,
				})
				drawer := &font.Drawer{Face: faceTitle}
				adv := drawer.MeasureString(timeDisplay)
				timeWidth := int(adv >> 6)
				rightMargin := width - xStart - 5
				timeX := rightMargin - timeWidth
				ptTime := freetype.Pt(timeX, int(y+fontSizeTitle))
				_, err = fcTitle.DrawString(timeDisplay, ptTime)
				if err != nil {
					fmt.Printf("Error drawing time: %v\n", err)
					return nil, err
				}
				y += fontSizeTitle * lineSpacing
			} else {
				pt := freetype.Pt(xStart, int(y+fontSize))
				_, err := fc.DrawString(line.Text, pt)
				if err != nil {
					fmt.Printf("Error drawing string: %v\n", err)
					return nil, err
				}
				y += fontSize * lineSpacing
			}
		}
		imgs = append(imgs, img)
		
	}
	margin := 4 // margin from the right and bottom
	pageNumFontSize := 10.0
	total := len(imgs)
    for i, im := range imgs {
        pageStr := fmt.Sprintf("%d/%d", i+1, total)
        facePN := truetype.NewFace(fnt, &truetype.Options{Size: pageNumFontSize, DPI: 72, Hinting: font.HintingFull})
        dr := &font.Drawer{
            Dst:  im.(draw.Image), // assert back to draw.Image
            Src:  image.NewUniform(color.RGBA{200, 200, 200, 255}),
            Face: facePN,
        }
        w := int(dr.MeasureString(pageStr) >> 6)
        x := width/2 - w/2 //center
        y := height - margin/2
        dr.Dot = fixed.P(x, y)
        dr.DrawString(pageStr)

		if savePng {
			fname := fmt.Sprintf("page_%d.png", i)
			f, err := os.Create(fname)
			if err != nil {
				fmt.Printf("Error creating file: %v\n", err)
				continue
			}
			if err := png.Encode(f, im); err != nil {
				fmt.Printf("Error encoding PNG: %v\n", err)
			}
			f.Close()
			fmt.Printf("Generated %s\n", fname)
		}
    }

    return imgs, nil
}

// isCJK reports whether r belongs to a CJK script.
func isCJK(r rune) bool {
    return unicode.In(r,
        unicode.Han,       // Chinese characters
        unicode.Hiragana,  // Japanese hiragana
        unicode.Katakana,  // Japanese katakana
        unicode.Hangul)    // Korean hangul
}

// wrapText splits text into lines that fit within maxWidth.
// - English/Latin words only break at spaces.
// - A single word that is too wide gets hyphenated.
// - CJK characters may break anywhere, and never get spaces around them.
func wrapText(text string, maxWidth int, face font.Face) []string {
    // Helper to detect CJK runes
    isCJK := func(r rune) bool {
        return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
    }

    // 1) Tokenize into either:
    //    - runs of non-CJK (i.e. potential words)
    //    - single CJK runes
    var tokens []string
    var buf []rune
    flush := func() {
        if len(buf)>0 {
            tokens = append(tokens, string(buf))
            buf = buf[:0]
        }
    }

    for _, r := range text {
        if unicode.IsSpace(r) {
            flush()
        } else if isCJK(r) {
            flush()
            tokens = append(tokens, string(r))
        } else {
            buf = append(buf, r)
        }
    }
    flush()

    // 2) Build lines
    var lines []string
    drawer := &font.Drawer{Face: face}
    current := ""

    for _, tok := range tokens {
        // decide separator: only a space if both neighbors are non-CJK
        sep := ""
        if current != "" {
            first := []rune(tok)[0]
            last  := []rune(current)[len([]rune(current))-1]
            if !isCJK(first) && !isCJK(last) {
                sep = " "
            }
        }

        candidate := current + sep + tok
        if int(drawer.MeasureString(candidate)>>6) <= maxWidth {
            current = candidate
            continue
        }

        // if overflow
        if current != "" {
            lines = append(lines, current)
            current = ""
        }

        // tok alone too wide?
        if int(drawer.MeasureString(tok)>>6) <= maxWidth {
            current = tok
        } else {
            // hyphenate non-CJK words, else break CJK one rune at a time
            runes := []rune(tok)
            if !isCJK(runes[0]) && len(runes)>1 {
                // hyphenate
                for i:=1; i<len(runes); i++ {
                    part := string(runes[:i]) + "-"
                    if int(drawer.MeasureString(part)>>6) > maxWidth {
                        lines = append(lines, string(runes[:i-1])+"-")
                        current = string(runes[i-1:])
                        break
                    }
                }
            } else {
                // CJK or single rune: char-by-char
                for _, r := range runes {
                    s := string(r)
                    if current=="" {
                        current = s
                    } else if int(drawer.MeasureString(current+s)>>6) <= maxWidth {
                        current += s
                    } else {
                        lines = append(lines, current)
                        current = s
                    }
                }
            }
        }
    }

    if current!="" {
        lines = append(lines, current)
    }
    return lines
}

