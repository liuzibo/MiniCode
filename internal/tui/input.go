package tui

import (
	"regexp"
	"strings"
)

const esc = "\x1b"

var ctrlMap = map[byte]string{
	'\x01': "a",
	'\x03': "c",
	'\x05': "e",
	'\x0e': "n",
	'\x0f': "o",
	'\x10': "p",
	'\x15': "u",
}

func ParseInputChunk(previousRest string, chunk string) ParseResult {
	input := previousRest + chunk
	events := []InputEvent{}
	index := 0
	for index < len(input) {
		remaining := input[index:]
		if strings.HasPrefix(remaining, esc) {
			if needMoreEscape(remaining) {
				return ParseResult{Events: events, Rest: remaining}
			}
			event, length, ok := parseEscape(remaining)
			if ok {
				if event.Kind != "" {
					events = append(events, event)
				}
				index += length
				continue
			}
		}
		ch := input[index]
		if ch >= 0x80 {
			seqLen := utf8SeqLen(ch)
			if seqLen > 1 {
				if index+seqLen > len(input) {
					return ParseResult{Events: events, Rest: input[index:]}
				}
				valid := true
				for i := 1; i < seqLen; i++ {
					if input[index+i]&0xc0 != 0x80 {
						valid = false
						break
					}
				}
				if valid {
					events = append(events, InputEvent{Kind: EventText, Text: input[index : index+seqLen]})
					index += seqLen
					continue
				}
			}
			index++
			continue
		}
		switch {
		case ch == '\r' || ch == '\n':
			events = append(events, InputEvent{Kind: EventKey, Name: KeyReturn})
		case ch == '\t':
			events = append(events, InputEvent{Kind: EventKey, Name: KeyTab})
		case ch == '\x7f' || ch == '\b':
			events = append(events, InputEvent{Kind: EventKey, Name: KeyBackspace})
		case ch >= '\x01' && ch <= '\x1a':
			if name, ok := ctrlMap[ch]; ok {
				events = append(events, InputEvent{Kind: EventText, Text: name, Ctrl: true})
			}
		case ch >= ' ':
			events = append(events, InputEvent{Kind: EventText, Text: string(ch)})
		}
		index++
	}
	return ParseResult{Events: events}
}

func utf8SeqLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b < 0xc0:
		return -1
	case b < 0xe0:
		return 2
	case b < 0xf0:
		return 3
	case b < 0xf8:
		return 4
	default:
		return -1
	}
}

func needMoreEscape(input string) bool {
	if input == esc {
		return true
	}
	if strings.HasPrefix(input, esc+"[") {
		return !regexp.MustCompile(`^\x1b\[[<\d;?]*[~A-Za-zMm]`).MatchString(input)
	}
	if strings.HasPrefix(input, esc+"O") && len(input) < 3 {
		return true
	}
	return false
}

func parseEscape(input string) (InputEvent, int, bool) {
	if match := regexp.MustCompile(`^\x1b\[(?:1;(\d+))?([ABCDHF])`).FindStringSubmatch(input); match != nil {
		nameMap := map[string]KeyName{"A": KeyUp, "B": KeyDown, "C": KeyRight, "D": KeyLeft, "H": KeyHome, "F": KeyEnd}
		event := InputEvent{Kind: EventKey, Name: nameMap[match[2]]}
		if match[1] == "3" {
			event.Meta = true
		}
		if match[1] == "5" {
			event.Ctrl = true
		}
		return event, len(match[0]), true
	}
	if match := regexp.MustCompile(`^\x1b\[(\d+)~`).FindStringSubmatch(input); match != nil {
		nameMap := map[string]KeyName{"1": KeyHome, "3": KeyDelete, "4": KeyEnd, "5": KeyPageUp, "6": KeyPageDown, "7": KeyHome, "8": KeyEnd}
		name, ok := nameMap[match[1]]
		if !ok {
			return InputEvent{}, len(match[0]), true
		}
		return InputEvent{Kind: EventKey, Name: name}, len(match[0]), true
	}
	if match := regexp.MustCompile(`^\x1bO([ABCDHF])`).FindStringSubmatch(input); match != nil {
		nameMap := map[string]KeyName{"A": KeyUp, "B": KeyDown, "C": KeyRight, "D": KeyLeft, "H": KeyHome, "F": KeyEnd}
		return InputEvent{Kind: EventKey, Name: nameMap[match[1]]}, len(match[0]), true
	}
	if strings.HasPrefix(input, esc+"\t") {
		return InputEvent{Kind: EventKey, Name: KeyTab, Meta: true}, 2, true
	}
	if len(input) >= 2 {
		ch := input[1]
		if ch != '[' && ch != 'O' {
			return InputEvent{Kind: EventText, Text: string(ch), Meta: true}, 2, true
		}
	}
	return InputEvent{Kind: EventKey, Name: KeyEscape}, 1, true
}
