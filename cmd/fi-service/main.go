// fi-service — служба FI: держит обход, проверяет сервисы и отвечает окну в трее.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"

	"fi/internal/daemon"
	"fi/internal/engine"
	"fi/internal/ipc"
)

const usage = `Использование:
  fi-service import-base <папка>   установить набор стратегий (zapret-discord-youtube)
  fi-service install               установить и запустить службу
  fi-service uninstall             остановить и удалить службу
  fi-service run                   запустить в консоли, для отладки

Папка данных: %s (переопределяется переменной FI_DATA).
Все команды — от имени администратора.
`

var version = "dev" // задаётся при сборке: -ldflags "-X main.version=…"

func main() {
	daemon.Version = version
	if runningAsService() {
		return
	}
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usage, dataDir())
		os.Exit(2)
	}

	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "run":
		err = runConsole()
	case "import-base":
		err = importBase(args)
	case "install":
		err = installService()
	case "uninstall":
		err = uninstallService()
	default:
		fmt.Fprintf(os.Stderr, usage, dataDir())
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}
}

func dataDir() string {
	if dir := os.Getenv("FI_DATA"); dir != "" {
		return dir
	}
	return defaultDataDir()
}

// serve запускает ядро и канал для окна и работает до отмены ctx.
func serve(ctx context.Context, log *slog.Logger) error {
	d, err := daemon.New(dataDir(), log)
	if err != nil {
		return err
	}
	l, err := ipc.Listen()
	if err != nil {
		return fmt.Errorf("канал для окна: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	serveErr := make(chan error, 1)
	go func() {
		err := ipc.Serve(ctx, l, d.Handle)
		if err != nil {
			log.Error("канал для окна закрылся", "err", err)
			cancel()
		}
		serveErr <- err
	}()

	log.Info("служба запущена", "data", dataDir())
	runErr := d.Run(ctx)
	cancel()
	if err := <-serveErr; runErr == nil {
		runErr = err
	}
	return runErr
}

func runConsole() error {
	if !engine.IsElevated() {
		return errors.New("запустите от имени администратора: движку нужен WinDivert")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return serve(ctx, slog.New(slog.NewTextHandler(os.Stderr, nil)))
}

func importBase(args []string) error {
	if len(args) != 1 {
		return errors.New("укажите папку набора: fi-service import-base <папка>")
	}
	if !engine.IsElevated() {
		return errors.New("запустите от имени администратора")
	}
	if serviceRunning() {
		return errors.New("остановите службу FI перед установкой набора")
	}
	n, err := daemon.ImportBase(dataDir(), args[0])
	if err != nil {
		return err
	}
	fmt.Printf("Набор установлен в %s, стратегий: %d.\n", filepath.Join(dataDir(), "base"), n)
	return nil
}
