// fi-sign — ключи и подпись обновлений FI.
//
//	fi-sign keygen <файл ключа>
//	    создаёт секретный ключ и печатает открытый — его передают сборке (-PublicKey)
//	fi-sign sign -key <файл> -version 0.2.0 -url https://…/fi-setup.exe -installer build\fi-setup.exe -out build\update.json
//	    пишет манифест и подпись к нему (update.json.sig); оба файла публикуются по адресу -UpdateURL
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"fi/internal/appupdate"
)

func main() {
	var err error
	switch {
	case len(os.Args) >= 2 && os.Args[1] == "keygen":
		err = keygen(os.Args[2:])
	case len(os.Args) >= 2 && os.Args[1] == "sign":
		err = sign(os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, "Использование: fi-sign keygen <файл ключа> | fi-sign sign -key … -version … -url … -installer … -out …")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}
}

func keygen(args []string) error {
	if len(args) != 1 {
		return errors.New("укажите файл для секретного ключа")
	}
	if _, err := os.Stat(args[0]); err == nil {
		return fmt.Errorf("%s уже есть — не перезаписываю, иначе старые сборки перестанут принимать обновления", args[0])
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.WriteFile(args[0], []byte(base64.StdEncoding.EncodeToString(priv.Seed())+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Printf("Секретный ключ: %s — храните отдельно, не публикуйте и не кладите в репозиторий.\n", args[0])
	fmt.Printf("Открытый ключ для сборки: %s\n", base64.StdEncoding.EncodeToString(pub))
	return nil
}

func sign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	keyFile := fs.String("key", "", "секретный ключ")
	version := fs.String("version", "", "версия")
	url := fs.String("url", "", "https-адрес установщика этой версии")
	installer := fs.String("installer", `build\fi-setup.exe`, "установщик")
	out := fs.String("out", `build\update.json`, "манифест")
	notes := fs.String("notes", "", "что нового")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyFile == "" || *version == "" || *url == "" {
		return errors.New("нужны -key, -version и -url")
	}

	seedText, err := os.ReadFile(*keyFile)
	if err != nil {
		return err
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(seedText)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return fmt.Errorf("%s: это не секретный ключ fi-sign", *keyFile)
	}
	priv := ed25519.NewKeyFromSeed(seed)

	data, err := os.ReadFile(*installer)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	m := appupdate.Manifest{
		Version:   *version,
		URL:       *url,
		SHA256:    hex.EncodeToString(sum[:]),
		Size:      int64(len(data)),
		Published: time.Now().UTC().Truncate(time.Second),
		Notes:     *notes,
	}
	manifest, sig, err := appupdate.Sign(priv, m)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, manifest, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(*out+".sig", sig, 0o644); err != nil {
		return err
	}
	fmt.Printf("Подписано: %s и %s (версия %s, открытый ключ %s)\n", *out, *out+".sig", m.Version,
		base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)))
	return nil
}
