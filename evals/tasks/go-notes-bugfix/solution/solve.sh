#!/bin/bash
# Restore the code point count that the seed replaced with a byte count.
set -euo pipefail
cd /app/notes
sed -i 's/len(input.Text) > 200/utf8.RuneCountInString(input.Text) > 200/' http.go
sed -i 's#^\t"strings"$#\t"strings"\n\t"unicode/utf8"#' http.go
gofmt -l . | (! grep .)
