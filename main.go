/*
 * Copyright (C) 2023 Andrea Fiori <andrea.fiori.1998@gmail.com>
 *
 * Licensed under GPLv3, see file LICENSE in this source tree.
 */

package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/eiannone/keyboard"
	"golang.org/x/term"
)

// command line arguments variables
var (
	csvFile string
	comma   string
)

type Pager struct {
	sync.Mutex
	horizontalShift    int
	maxWidth           []int
	file               *os.File
	prevPrintableLines []string
}

var (
	pager = Pager{}
)

func main() {
	// handle command line arguments
	flag.StringVar(&csvFile, "file", "", "Path to the CSV file")
	flag.StringVar(&comma, "comma", ",", "Field delimiter in the CSV file")

	flag.Parse()

	if csvFile == "" {
		flag.Usage()
		os.Exit(1)
	}

	// open csv file
	file, err := os.Open(csvFile)
	fatalIfErr(err)
	defer file.Close()
	pager.file = file

	pager.hideCursor()
	defer pager.showCursor()
	defer fmt.Println()

	// first time render
	pager.renderCSVWindow()

	// handle terminal resize
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGWINCH, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	go handleSignals(sigChan)

	err = keyboard.Open()
	fatalIfErr(err)
	defer func() {
		_ = keyboard.Close()
	}()

	keyboardHandler()
}

// handlers

func handleSignals(sigChan chan os.Signal) {
	for sig := range sigChan {
		switch sig {
		case syscall.SIGWINCH:
			pager.Lock()
			pager.renderCSVWindow()
			pager.Unlock()
		default:
			// cleanup
			pager.showCursor()
			fmt.Println()
		}
	}
}

func keyboardHandler() {
outerLoop:
	for {
		ch, key, err := keyboard.GetKey()
		fatalIfErr(err)
		pager.Lock()

		if unicode.IsDigit(ch) {
			handleLineJump(ch)
		} else {
			switch key {
			case keyboard.KeyArrowUp:
				pager.seekBackwardN(1)
			case keyboard.KeyArrowDown:
				pager.seekForwardN(1)
			case keyboard.KeyArrowLeft:
				pager.moveToLeft()
			case keyboard.KeyArrowRight:
				pager.moveToRight()
			case keyboard.KeyCtrlN:
				pager.seekForwardN(pager.countLinesFitting())
			case keyboard.KeyCtrlP:
				pager.seekBackwardN(pager.countLinesFitting())
			case keyboard.KeySpace:
				pager.seekForwardN(pager.countLinesFitting() / 2)
			case keyboard.KeyCtrlC:
				break outerLoop
			}
			switch ch {
			case 'j':
				pager.seekForwardN(1)
			case 'k':
				pager.seekBackwardN(1)
			case 'l':
				pager.moveToRight()
			case 'h':
				pager.moveToLeft()
			case 'q':
				break outerLoop
			case 'G':
				pager.seekEnd()
			case 'g':
				pager.seekStart()
			}
		}
		pager.renderCSVWindow()

		pager.Unlock()
	}
}

func handleLineJump(digit rune) {
	pager.clearLastScreenLine()
	buffer := []rune{digit}
	fmt.Printf("%c", digit)
	for {
		ch, key, err := keyboard.GetKey()
		fatalIfErr(err)
		if unicode.IsDigit(ch) {
			fmt.Printf("%c", ch)
			buffer = append(buffer, ch)
			continue
		}
		n, _ := strconv.Atoi(string(buffer))
		switch key {
		case keyboard.KeyArrowDown:
			pager.seekForwardN(n)
		case keyboard.KeyArrowUp:
			pager.seekBackwardN(n)
		}
		switch ch {
		case 'j':
			pager.seekForwardN(n)
		case 'k':
			pager.seekBackwardN(n)
		}
		return
	}
}

// pager

func (p *Pager) readLines() [][]string {
	res := [][]string{}
	s, err := p.file.Seek(0, io.SeekCurrent)
	defer func() {
		_, err = p.file.Seek(s, io.SeekStart)
		fatalIfErr(err)
	}()
	fatalIfErr(err)
	reader := getNewCSVReader(p.file)
	for i := 0; i < p.countLinesFitting(); i++ {
		record, err := reader.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			log.Fatalln(err)
		}
		for len(record) > len(p.maxWidth) {
			p.maxWidth = append(p.maxWidth, 0)
		}
		for f, field := range record {
			n := utf8.RuneCountInString(field)
			p.maxWidth[f] = max(p.maxWidth[f], n)
		}
		res = append(res, record)
	}
	return res
}

func (p *Pager) getPrintableLines(lines [][]string) []string {
	width, height := p.termSize()
	header := p.getTableHeaderLine()
	dataFooter := p.getDataLine()
	footer := p.getTableFooterLine()
	window := []string{}
	window = append(window, header)
	for _, record := range lines {
		if len(window) >= height {
			break
		}
		dataFields := p.getDataFields(record)
		window = append(window, dataFields)
		window = append(window, dataFooter)
	}
	window[len(window)-1] = footer

	// truncate for height
	window = window[:min(height, len(window))]

	// truncate for width
	for i, pl := range window {
		rpl := []rune(pl)
		truncated := string(rpl[p.horizontalShift:min(len(rpl), p.horizontalShift+width)])
		window[i] = truncated
	}

	return window
}

func (p *Pager) renderCSVWindow() {
	p.cursorTopLeftTerm()
	lines := p.readLines()
	printableLines := p.getPrintableLines(lines)
	if p.prevPrintableLines == nil ||
		len(printableLines) != len(p.prevPrintableLines) ||
		(len(printableLines) > 0 && len(printableLines[0]) != len(p.prevPrintableLines[0])) {
		p.clearTerm()
	}
	p.prevPrintableLines = printableLines
	fmt.Print(strings.Join(printableLines, "\n"))
}

func (p *Pager) getTableLine(start, middle, end string) string {
	var builder strings.Builder
	j := 0
	length := p.countMaxJoinedLength()
	for i := 0; i < length; i++ {
		s := ""
		switch {
		case i == 0:
			s = start
		case i == length-1:
			s = end
		case sum(p.maxWidth[0:j+1])+(j+1) == i:
			s = middle
			j++
		default:
			s = "━"
		}
		builder.WriteString(s)
	}
	return builder.String()
}

func (p *Pager) getTableHeaderLine() string {
	return p.getTableLine("┏", "┳", "┓")
}

func (p *Pager) getTableFooterLine() string {
	return p.getTableLine("┗", "┻", "┛")
}

func (p *Pager) getDataLine() string {
	return p.getTableLine("┣", "╋", "┫")
}

func (p *Pager) getDataFields(line []string) string {
	var builder strings.Builder
	builder.WriteString("┃")
	for i, field := range line {
		builder.WriteString(p.padString(field, p.maxWidth[i]))
		builder.WriteString("┃")
	}
	return builder.String()
}

func (p *Pager) countMaxJoinedLength() int {
	res := 0
	for _, w := range p.maxWidth {
		res += w + 1
	}
	return res + 1
}

func (p *Pager) countLinesFitting() int {
	_, height := p.termSize()
	return height / 2
}

func (p *Pager) moveToLeft() {
	i := p.currentFieldIndex()
	if i == 0 {
		p.horizontalShift = 0
		return
	}
	p.horizontalShift = max(0, p.horizontalShift-p.maxWidth[i-1]-1)
}

func (p *Pager) moveToRight() {
	width, _ := p.termSize()
	length := p.countMaxJoinedLength()
	if length-p.horizontalShift <= width {
		return
	}
	i := p.currentFieldIndex()
	p.horizontalShift = p.horizontalShift + p.maxWidth[i] + 1
}

func (p *Pager) currentFieldIndex() int {
	i := 0
	cum := 0
	for _, w := range p.maxWidth {
		if p.horizontalShift-i >= cum && p.horizontalShift-i < cum+w {
			return i
		}
		cum += w
		i++
	}

	if p.horizontalShift == cum {
		return len(p.maxWidth) - 1
	}
	return 0
}

func (p *Pager) padString(input string, length int) string {
	n := utf8.RuneCountInString(input)
	if n == length {
		return input
	}
	paddedString := strings.Repeat(" ", length-n) + input
	return paddedString
}

func (p *Pager) seekBackLine() int64 {
	res, err := p.file.Seek(0, io.SeekCurrent)
	fatalIfErr(err)
	b := make([]byte, 1)
	enteredNL := false
	enteredText := false
	res--
	for {
		if res <= 0 {
			_, err = p.file.Seek(0, io.SeekStart)
			fatalIfErr(err)
			return 0
		}
		_, err = p.file.ReadAt(b, res)
		fatalIfErr(err)
		if b[0] == '\n' && !enteredNL {
			// skip initial new lines
			enteredNL = true
		} else if b[0] != '\n' && enteredNL {
			// skip line before initial new lines
			enteredText = true
		} else if b[0] == '\n' && enteredText {
			res++
			_, err = p.file.Seek(res, io.SeekStart)
			fatalIfErr(err)
			return res
		}
		res--
	}
}

func (p *Pager) seekForwardLine() int64 {
	res, err := p.file.Seek(0, io.SeekCurrent)
	fatalIfErr(err)
	b := make([]byte, 1)
	enteredNL := false
	for {
		_, err = p.file.ReadAt(b, res)
		if err != nil {
			if err == io.EOF {
				res, err = p.file.Seek(0, io.SeekEnd)
				fatalIfErr(err)
				return res
			}
			fatalIfErr(err)
		}
		fatalIfErr(err)
		if b[0] == '\n' && !enteredNL {
			enteredNL = true
		} else if b[0] != '\n' && enteredNL {
			_, err := p.file.Seek(res, io.SeekStart)
			fatalIfErr(err)
			return res
		}
		res++
	}
}

func (p *Pager) countLinesAvailable() int {
	s, err := p.file.Seek(0, io.SeekCurrent)
	fatalIfErr(err)
	defer func() {
		_, err = p.file.Seek(s, io.SeekStart)
		fatalIfErr(err)
	}()
	reader := getNewCSVReader(p.file)
	for i := 0; i < p.countLinesFitting(); i++ {
		_, err := reader.Read()
		if err != nil {
			if err == io.EOF {
				return i
			}
			log.Fatalln(err)
		}
	}
	return p.countLinesFitting()
}

func (p *Pager) seekForwardN(n int) {
	lines := p.countLinesAvailable()
	for i := 0; i < n; i++ {
		p.seekForwardLine()
	}
	for p.countLinesAvailable() < lines {
		p.seekBackLine()
	}
}

func (p *Pager) seekBackwardN(n int) {
	for i := 0; i < n; i++ {
		p.seekBackLine()
	}
}

func (p *Pager) seekStart() {
	_, err := p.file.Seek(0, io.SeekStart)
	fatalIfErr(err)
}

func (p *Pager) seekEnd() {
	_, err := p.file.Seek(0, io.SeekEnd)
	fatalIfErr(err)
	for i := 0; i < p.countLinesFitting(); i++ {
		p.seekBackLine()
	}
}

func (p *Pager) cursorTopLeftTerm() {
	fmt.Print("\033[H")
}

func (p *Pager) clearTerm() {
	fmt.Print("\033[H\033[2J")
}

func (p *Pager) hideCursor() {
	fmt.Print("\033[?25l")
}

func (p *Pager) showCursor() {
	fmt.Print("\033[?25h")
}

func (p *Pager) termSize() (width int, height int) {
	fd := int(os.Stdout.Fd())
	width, height, err := term.GetSize(fd)
	fatalIfErr(err)
	return width, height
}

func (p *Pager) clearLastScreenLine() {
	width, _ := pager.termSize()
	fmt.Printf("\r")
	for i := 0; i < width; i++ {
		fmt.Print(" ")
	}
	fmt.Printf("\r")
}

// util

func sum(ns []int) int {
	res := 0
	for _, n := range ns {
		res += n
	}
	return res
}

func min(x, y int) int {
    if x < y {
        return x
    }
    return y
}

func max(x, y int) int {
    if x > y {
        return x
    }
    return y
}

func getNewCSVReader(file *os.File) *csv.Reader {
	reader := csv.NewReader(file)
	reader.Comma = rune(comma[0])
	reader.LazyQuotes = true
	reader.FieldsPerRecord = -1
	return reader
}

func fatalIfErr(err error) {
	if err != nil {
		panic(err)
	}
}
