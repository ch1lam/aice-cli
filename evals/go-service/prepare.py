#!/usr/bin/env python3
"""Materialize an evaluator-owned starting point without duplicating source trees."""
import argparse
from pathlib import Path
import re
import shutil
import subprocess

ROOT = Path(__file__).resolve().parent


def replace_once(text, old, new):
    if text.count(old) != 1:
        raise ValueError(f"seed recipe drift: expected one occurrence of {old!r}")
    return text.replace(old, new, 1)


def without_limit(http, store):
    start = http.index('\t\tif values, present := r.URL.Query()["limit"]; present {')
    end = http.index('\n\t\twriteJSON(w, http.StatusOK', start)
    http = http[:start] + http[end:]
    http = replace_once(http, '\t\tlimit := 0\n', '')
    http = replace_once(http, 's.list(tag, limit)', 's.list(tag)')
    store = replace_once(store, 'list(tag string, limit int)', 'list(tag string)')
    store = replace_once(store, '\tif limit > 0 && len(items) > limit {\n\t\titems = items[:limit]\n\t}\n', '')
    return http, store


def prepare(kind, destination):
    destination = Path(destination)
    if destination.exists():
        raise ValueError(f"destination must not exist: {destination}")
    if kind == "greenfield":
        destination.mkdir(parents=True)
        shutil.copyfile(ROOT / "reference/go.mod", destination / "go.mod")
        return
    shutil.copytree(ROOT / "reference", destination)
    hp, sp = destination / "notes/http.go", destination / "notes/store.go"
    http, store = hp.read_text(), sp.read_text()
    if kind != "reference":
        http, store = without_limit(http, store)
    if kind == "extension":
        # Stage 1 completed, with neither tag input nor filtering implemented.
        http = re.sub(r'\n\s*Tag\s+string `json:"tag"`', '', http, count=1)
        http = replace_once(http, ' || !validTag(input.Tag)', '')
        http = replace_once(http, 's.add(input.Text, input.Tag)', 's.add(input.Text)')
        start = http.index('\t\ttag := r.URL.Query().Get("tag")')
        end = http.index('\n\t\twriteJSON(w, http.StatusOK', start)
        http = http[:start] + http[end:]
        http = replace_once(http, 's.list(tag)', 's.list()')
        start, end = http.index('func validTag('), http.index('func writeJSON(')
        http = http[:start] + http[end:]
        store = re.sub(r'\n\s*Tag\s+string `json:"tag"`', '', store, count=1)
        store = replace_once(store, 'add(text, tag string)', 'add(text string)')
        store = replace_once(store, ', Tag: tag', '')
        store = replace_once(store, 'list(tag string)', 'list()')
        store = replace_once(store, '\t\tif tag == "" || n.Tag == tag {\n\t\t\titems = append(items, n)\n\t\t}', '\t\titems = append(items, n)')
    if kind == "bug":
        http = replace_once(http, 'utf8.RuneCountInString(input.Text)', 'len(input.Text)')
        http = replace_once(http, '\n\t"unicode/utf8"', '')
    if kind == "refactor":
        # Mix collection ownership and routing inside one constructor. No hidden bug.
        body = store[store.index('func (s *store) add'):]
        body = body.replace('func (s *store) add', 'add := func')
        body = body.replace('func (s *store) list', 'list := func')
        body = body.replace('func (s *store) delete', 'deleteNote := func')
        body = body.replace('s.mu', 'mu').replace('s.nextID', 'nextID').replace('s.notes', 'records')
        body = '\n'.join('\t' + line if line else '' for line in body.splitlines())
        note_type = store[store.index('type note struct'):store.index('// store owns')]
        http = replace_once(http, 'import (', 'import (\n\t"sort"\n\t"sync"')
        http = replace_once(http, '// NewHandler', note_type + '// NewHandler')
        http = replace_once(http, '\ts := &store{notes: make(map[int]note)}', '\tvar mu sync.Mutex\n\tnextID := 0\n\trecords := make(map[int]note)\n' + body)
        http = http.replace('s.add(', 'add(').replace('s.list(', 'list(').replace('s.delete(', 'deleteNote(')
        sp.unlink()
    else:
        sp.write_text(store)
    hp.write_text(http)
    subprocess.run(['gofmt', '-w', str(destination / 'notes')], check=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('kind', choices=['greenfield', 'extension', 'bug', 'refactor', 'reference'])
    parser.add_argument('destination', type=Path)
    args = parser.parse_args()
    prepare(args.kind, args.destination)
