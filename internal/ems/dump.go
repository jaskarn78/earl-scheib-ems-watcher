package ems

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/Valentin-Kaiser/go-dbase/dbase"
)

// DumpAllFields opens the dBase file at path read-only and writes a human-
// readable dump of every column in row 0 to out. Diagnostic-only — used by the
// `earlscheib --dump-bundle` subcommand to show what CCC ONE actually writes,
// so we can identify which field carries close/lock state without guessing.
//
// Output shape (one block per file):
//
//	=== <basename.ext> (rows=N, cols=M) ===
//	  COL_NAME           [TYPE/LEN]  =  <trimmed value or <MEMO> for memo cells>
//	  ...
//
// Memo (type M) cells are rendered as `<MEMO>` because the .FPT/.DBT sidecar
// is not always present and we don't need memo content to spot status fields.
// Empty tables (RecordsCount==0) print `(empty table)` and skip the row dump.
func DumpAllFields(path string, out io.Writer) error {
	tbl, err := dbase.OpenTable(&dbase.Config{
		Filename:   path,
		TrimSpaces: true,
		ReadOnly:   true,
		Untested:   true, // CCC ONE writes FoxBasePlus (0x03)
	})
	if err != nil {
		return fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	defer tbl.Close()

	cols := tbl.Columns()
	rowCount := tbl.Header().RecordsCount()

	fmt.Fprintf(out, "=== %s (rows=%d, cols=%d) ===\n",
		filepath.Base(path), rowCount, len(cols))

	if rowCount == 0 {
		fmt.Fprintln(out, "  (empty table)")
		fmt.Fprintln(out)
		return nil
	}

	raw, err := tbl.ReadRow(0)
	if err != nil {
		return fmt.Errorf("read row 0 of %s: %w", filepath.Base(path), err)
	}
	if len(raw) < 1 {
		fmt.Fprintln(out, "  (row 0 truncated)")
		fmt.Fprintln(out)
		return nil
	}

	// Walk columns at fixed offsets; first byte is the delete marker.
	offset := uint16(1)
	for _, col := range cols {
		length := uint16(col.Length)
		dtype := dbase.DataType(col.DataType)
		typeTag := fmt.Sprintf("%c/%d", byte(dtype), length)

		if int(offset)+int(length) > len(raw) {
			fmt.Fprintf(out, "  %-18s [%s]  =  <ROW TRUNCATED>\n",
				col.Name(), typeTag)
			break
		}

		// Skip memo cells — sidecar resolution may fail and we don't need
		// memo content to identify status fields.
		if dtype == dbase.Memo {
			fmt.Fprintf(out, "  %-18s [%s]  =  <MEMO>\n",
				col.Name(), typeTag)
			offset += length
			continue
		}

		cellBytes := raw[offset : offset+length]
		offset += length

		val, ierr := tbl.Interpret(cellBytes, col)
		if ierr != nil {
			fmt.Fprintf(out, "  %-18s [%s]  =  <interpret err: %v>\n",
				col.Name(), typeTag, ierr)
			continue
		}
		display := strings.TrimSpace(fmt.Sprint(val))
		if display == "" {
			display = `""`
		}
		fmt.Fprintf(out, "  %-18s [%s]  =  %s\n",
			col.Name(), typeTag, display)
	}
	fmt.Fprintln(out)
	return nil
}
