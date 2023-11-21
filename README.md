# csv-peek

csv-peek is a lightweight CSV file viewer for the command line, designed to simplify exploration and navigation of CSV data.

## Installation

```
go install github.com/fioriandrea/csv-peek@latest
```

## Keyboard Shortcuts

- Arrow keys: Navigate up, down, left, and right.
- `j`: Move forward one line.
- `k`: Move backward one line.
- `l`: Move one character to the right.
- `h`: Move one character to the left.
- `Ctrl+N`: Move forward by a page.
- `Ctrl+P`: Move backward by a page.
- `Space`: Move forward by half a page.
- `Ctrl+C` or `q`: Quit the program.
- `G`: Go to the end of the file.
- `g`: Go to the beginning of the file.
- Digit keys + UP or DOWN: Jump N lines (non-optimized).

## License

This program is licensed under the GNU General Public License v3.0 (GPLv3). See the [LICENSE](LICENSE) file for details.