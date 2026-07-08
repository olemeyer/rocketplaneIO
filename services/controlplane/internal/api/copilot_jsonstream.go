package api

// copilot_jsonstream.go — inkrementeller Extraktor für EIN String-Feld aus
// einem JSON-Objekt, das als Delta-Strom ankommt (partial_json der Tool-Args).
// Der Master spricht mit dem Nutzer ausschließlich über respond{message}; damit
// die Antwort trotzdem Token-für-Token im UI erscheint, zieht diese kleine
// Tokenizer-State-Machine das message-Feld ohne vollständiges Parsen aus dem
// Strom und emittiert es entescaped. Kein Substring-Matching: Keys und Werte
// werden über Depth-/String-Tracking unterschieden, ein "message"-Vorkommen in
// einem anderen Feldwert triggert nicht.

import (
	"strconv"
	"strings"
)

type jsonFieldStreamer struct {
	field string
	emit  func(text string)

	depth      int             // {}/[]-Tiefe (Top-Level-Objekt = 1)
	inStr      bool            // in irgendeinem Nicht-Ziel-String
	esc        bool            // letztes Zeichen war '\' (in inStr oder capturing)
	uni        string          // \uXXXX-Sammler ("" = nicht aktiv), nur in capturing
	afterColon bool            // auf depth 1: zwischen ':' und ','
	lastKey    string          // zuletzt abgeschlossener Key auf depth 1
	keyBuf     strings.Builder // sammelt Key-Zeichen auf depth 1
	capturing  bool            // im Ziel-Feldwert
	done       bool
	emitted    int
}

func newJSONFieldStreamer(field string, emit func(string)) *jsonFieldStreamer {
	return &jsonFieldStreamer{field: field, emit: emit}
}

// Feed konsumiert das nächste Delta des JSON-Stroms.
func (j *jsonFieldStreamer) Feed(delta string) {
	if j.done || delta == "" {
		return
	}
	var out strings.Builder
	for i := 0; i < len(delta); i++ {
		c := delta[i]

		if j.capturing {
			if j.uni != "" || (j.esc && c == 'u') {
				if j.esc && c == 'u' {
					j.esc = false
					j.uni = "u"
					continue
				}
				j.uni += string(c)
				if len(j.uni) == 5 { // "u" + 4 Hexziffern
					if r, err := strconv.ParseUint(j.uni[1:], 16, 32); err == nil {
						out.WriteRune(rune(r))
					}
					j.uni = ""
				}
				continue
			}
			if j.esc {
				j.esc = false
				switch c {
				case 'n':
					out.WriteByte('\n')
				case 't':
					out.WriteByte('\t')
				case 'r':
					out.WriteByte('\r')
				case '"', '\\', '/':
					out.WriteByte(c)
				case 'b', 'f':
					// fürs UI irrelevant
				default:
					out.WriteByte(c)
				}
				continue
			}
			switch c {
			case '\\':
				j.esc = true
			case '"':
				j.done = true
				j.flush(out.String())
				return
			default:
				out.WriteByte(c)
			}
			continue
		}

		if j.inStr {
			if j.esc {
				j.esc = false
				if j.depth == 1 && !j.afterColon {
					j.keyBuf.WriteByte(c) // vereinfacht; Keys enthalten praktisch keine Escapes
				}
				continue
			}
			switch c {
			case '\\':
				j.esc = true
			case '"':
				j.inStr = false
				if j.depth == 1 && !j.afterColon {
					j.lastKey = j.keyBuf.String()
				}
				j.keyBuf.Reset()
			default:
				if j.depth == 1 && !j.afterColon {
					j.keyBuf.WriteByte(c)
				}
			}
			continue
		}

		switch c {
		case '"':
			if j.depth == 1 && j.afterColon && j.lastKey == j.field {
				j.capturing = true // Ziel-Feldwert beginnt
			} else {
				j.inStr = true
			}
		case ':':
			if j.depth == 1 {
				j.afterColon = true
			}
		case ',':
			if j.depth == 1 {
				j.afterColon = false
				j.lastKey = ""
			}
		case '{', '[':
			j.depth++
		case '}', ']':
			j.depth--
		}
	}
	j.flush(out.String())
}

func (j *jsonFieldStreamer) flush(text string) {
	if text == "" {
		return
	}
	j.emitted += len(text)
	j.emit(text)
}

// Emitted meldet, wie viele Zeichen bereits gestreamt wurden — 0 am Block-Ende
// ist das Fallback-Signal, die message komplett aus dem geparsten JSON zu emittieren.
func (j *jsonFieldStreamer) Emitted() int { return j.emitted }

// Done markiert den Streamer als abgeschlossen (Block-Ende).
func (j *jsonFieldStreamer) Done() { j.done = true }
