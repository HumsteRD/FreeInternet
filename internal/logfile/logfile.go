// Package logfile — журнал в файле с ограничением размера: переполненный журнал
// переименовывается в .old (прежний .old удаляется), и запись продолжается в новый файл.
package logfile

import (
	"os"
	"path/filepath"
	"sync"
)

// MaxSize — размер журнала по умолчанию: с .old на диске не больше вдвое больше.
const MaxSize = 5 << 20

type File struct {
	mu   sync.Mutex
	path string
	max  int64
	f    *os.File
	size int64
}

// Open открывает журнал path для дописывания.
func Open(path string, max int64) (*File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	l := &File{path: path, max: max}
	if fi, err := os.Stat(path); err == nil && fi.Size() >= max {
		os.Rename(path, path+".old")
	}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *File) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	l.f, l.size = f, 0
	if fi, err := f.Stat(); err == nil {
		l.size = fi.Size()
	}
	return nil
}

func (l *File) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size > 0 && l.size+int64(len(p)) > l.max {
		l.rotate()
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}

// rotate переносит полный журнал в .old. Если файл занят (его читают), запись продолжается
// в прежний файл, и попытка повторится со следующей строкой.
func (l *File) rotate() {
	l.f.Close()
	if os.Rename(l.path, l.path+".old") != nil {
		l.max += l.max / 10 // не пытаться на каждой строке
	}
	if l.open() != nil {
		l.f, _ = os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	}
}

func (l *File) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}
