package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"fi/internal/logfile"
)

const serviceName = "FI"

func defaultDataDir() string {
	return filepath.Join(os.Getenv("ProgramData"), "FI")
}

// runningAsService запускает службу, если процесс стартовал через диспетчер служб.
func runningAsService() bool {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return false
	}
	if err := svc.Run(serviceName, &service{}); err != nil {
		os.Exit(1)
	}
	return true
}

type service struct{}

func (s *service) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}
	logFile, log := openLog()
	if logFile != nil {
		defer logFile.Close()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serve(ctx, log) }()
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case err := <-done:
			if err != nil {
				log.Error("служба остановилась с ошибкой", "err", err)
				return false, 1
			}
			return false, 0
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				changes <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending, WaitHint: 20000}
				cancel()
				<-done
				log.Info("служба остановлена")
				return false, 0
			}
		}
	}
}

// openLog открывает журнал службы; переполненный журнал сохраняется рядом как .old.
func openLog() (*logfile.File, *slog.Logger) {
	f, err := logfile.Open(filepath.Join(dataDir(), "fi-service.log"), logfile.MaxSize)
	if err != nil {
		return nil, slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return f, slog.New(slog.NewTextHandler(f, nil))
}

func installService() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("диспетчер служб: %w", err)
	}
	defer m.Disconnect()

	if s, err := m.OpenService(serviceName); err == nil {
		s.Close()
		return errors.New("служба уже установлена")
	}
	s, err := m.CreateService(serviceName, exe, mgr.Config{
		DisplayName: "FI",
		Description: "Обход блокировок и замедлений: держит стратегию обхода и проверяет сервисы.",
		StartType:   mgr.StartAutomatic,
	})
	if err != nil {
		return err
	}
	defer s.Close()

	// Если служба упала — перезапустить: без неё пропадает обход.
	actions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 2 * time.Minute},
	}
	if err := s.SetRecoveryActions(actions, uint32((24 * time.Hour).Seconds())); err != nil {
		return err
	}
	if err := s.Start(); err != nil {
		return err
	}
	fmt.Println("Служба FI установлена и запущена.")
	return nil
}

func uninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("диспетчер служб: %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("служба не установлена: %w", err)
	}
	defer s.Close()

	if st, err := s.Query(); err == nil && st.State != svc.Stopped {
		if _, err := s.Control(svc.Stop); err != nil {
			return err
		}
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(300 * time.Millisecond) {
			if st, err := s.Query(); err == nil && st.State == svc.Stopped {
				break
			}
		}
	}
	if err := s.Delete(); err != nil {
		return err
	}
	fmt.Println("Служба FI удалена.")
	return nil
}

func serviceRunning() bool {
	m, err := mgr.Connect()
	if err != nil {
		return false
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return false
	}
	defer s.Close()
	st, err := s.Query()
	return err == nil && st.State != svc.Stopped
}
