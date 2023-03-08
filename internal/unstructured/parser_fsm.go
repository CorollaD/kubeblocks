/*
Copyright ApeCloud, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package unstructured

import (
	"fmt"
	"unicode/utf8"
)

// stateHandleFn represents the state of the scanner as a function that returns the next state.
type stateHandleFn func(*fsm) stateHandleFn

// fsm is finite state machine model of the redis configuration parser.
// The output of fsm is a parameter name and list of parameter values.
// reference c++ code implementation: https://github.com/redis/redis/blob/unstable/src/sds.c#L1082
type fsm struct {
	param *item

	// split chars
	splitCharacters string

	// the string being scanned
	input        string
	currentPos   int // current position in the input
	lastScanWith int // width of last rune read from input

	err error
	// scanned token
	token []rune
}

func (f *fsm) Parse(line string) error {
	f.input = line

	for handle := prepareScan(f); handle != nil; {
		handle = handle(f)
	}
	return f.err
}

func (f *fsm) empty() bool {
	return f.currentPos >= len(f.input)
}

func (f *fsm) next() rune {
	if f.empty() {
		f.lastScanWith = 0
		return eof
	}
	r, w := utf8.DecodeRuneInString(f.input[f.currentPos:])
	f.lastScanWith = w
	f.currentPos += f.lastScanWith
	return r
}

func (f *fsm) appendRune(r rune) {
	f.token = append(f.token, r)
}

func (f *fsm) peek() rune {
	r := f.next()
	f.currentPos -= f.lastScanWith
	return r
}

func prepareScan(f *fsm) stateHandleFn {
	switch r := f.next(); {
	case isEOF(r):
		return stateEOF
	case isSplitCharacter(r):
		return stateTokenEnd
	case isQuotes(r):
		return stateQuotesString
	case isSingleQuotes(r):
		return stateSQuotesString
	default:
		f.appendRune(r)
		return prepareScan
	}
}

func stateFailed(f *fsm, format string, args ...interface{}) stateHandleFn {
	f.err = fmt.Errorf(format, args...)
	return nil
}

func stateEOF(f *fsm) stateHandleFn {
	if len(f.token) > 0 {
		stateTokenEnd(f)
	}
	return nil
}

func stateTokenEnd(f *fsm) stateHandleFn {
	f.param.addToken(string(f.token))
	f.token = f.token[:0]
	return stateTrimSplitCharacters
}

func stateTrimSplitCharacters(f *fsm) stateHandleFn {
	for r := f.peek(); !isEOF(r) && isSplitCharacter(r); {
		f.next()
		r = f.peek()
	}
	return prepareScan
}

func stateQuotesString(f *fsm) stateHandleFn {
	switch r := f.next(); {
	default:
		f.appendRune(r)
	case isEOF(r):
		return stateFailed(f, "unterminated quotes.")
	case isQuotes(r):
		n := f.peek()
		if !isEOF(n) && !isSplitCharacter(n) {
			return stateFailed(f, "closing quote must be followed by a space or end.")
		}
		return stateTokenEnd
	case isEscape(r):
		if isEOF(f.peek()) {
			return stateFailed(f, "unterminated quotes.")
		}
		stateEscape(f)
	}
	return stateQuotesString
}

func stateEscape(f *fsm) {
	r := f.next()
	switch {
	default:
		f.appendRune(r)
	case isEOF(r):
		// do nothing
	case r == 'n':
		f.appendRune('\n')
	case r == 'r':
		f.appendRune('\r')
	case r == 't':
		f.appendRune('\t')
	case r == 'b':
		f.appendRune('\b')
	case r == 'a':
		f.appendRune('\a')
	}
}

func stateSQuotesString(f *fsm) stateHandleFn {
	switch r := f.next(); {
	default:
		f.appendRune(r)
	case isEOF(r):
		return stateFailed(f, "unterminated single quotes.")
	case isSingleQuotes(r):
		n := f.peek()
		if !isEOF(n) && !isSplitCharacter(n) {
			return stateFailed(f, "closing quote must be followed by a space or end.")
		}
		return stateTokenEnd
	case isEscape(r):
		if isSingleQuotes(f.peek()) {
			f.appendRune(singleQuotes)
			f.next()
		}
	}
	return stateSQuotesString
}
