package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Handle выполняет запрос окна. Методы, меняющие состояние, возвращают свежий статус,
// чтобы окно перерисовалось без лишнего запроса. Изменение выполняется до снимка статуса:
// в return оба выражения вычисляются слева направо.
func (d *Daemon) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	var on struct {
		On bool `json:"on"`
	}
	var host struct {
		Host  string `json:"host"`
		Force bool   `json:"force"` // добавить, даже если сайт открывается
	}

	var err error
	switch method {
	case "status":
	case "set_enabled":
		if err = decode(params, &on); err == nil {
			err = d.SetEnabled(on.On)
		}
	case "check":
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		_, err = d.Check(checkCtx)
		cancel()
		if err != nil {
			return nil, err
		}
	case "select":
		err = d.StartAutoSelect()
	case "set_strategy":
		var p struct {
			Name string `json:"name"`
		}
		if err = decode(params, &p); err == nil {
			err = d.SetStrategy(p.Name)
		}
	case "cancel":
		d.CancelTask()
	case "add_site":
		if err := decode(params, &host); err != nil {
			return nil, err
		}
		siteCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		res, err := d.AddSite(siteCtx, host.Host, host.Force)
		if err == nil && res.Added {
			d.recheckSoon() // иначе плитка «Сайты» до следующей проверки показывает «Пусто»
		}
		return res, err
	case "import_sites":
		var p struct {
			Sites []ImportEntry `json:"sites"`
		}
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		added, err := d.ImportSites(p.Sites)
		if err != nil {
			return nil, err
		}
		return map[string]any{"added": added, "status": d.Status()}, nil
	case "remove_site":
		if err = decode(params, &host); err == nil {
			if err = d.RemoveSite(host.Host); err == nil {
				d.recheckSoon()
			}
		}
	case "set_game_mode":
		var p struct {
			Mode string `json:"mode"`
		}
		if err = decode(params, &p); err == nil {
			err = d.SetGameMode(p.Mode)
		}
	case "set_auto_fix":
		if err = decode(params, &on); err == nil {
			err = d.SetAutoFix(on.On)
		}
	case "set_telegram":
		if err = decode(params, &on); err == nil {
			err = d.SetTelegram(on.On)
		}
	case "set_telegram_shared_cf":
		if err = decode(params, &on); err == nil {
			err = d.SetTelegramSharedCF(on.On)
		}
	case "set_telegram_worker":
		var p struct {
			Host string `json:"host"`
		}
		if err = decode(params, &p); err == nil {
			err = d.SetTelegramWorker(p.Host)
		}
	case "check_base":
		err = d.CheckBaseUpdate(ctx)
	case "update_base":
		err = d.StartBaseUpdate()
	case "set_auto_update":
		if err = decode(params, &on); err == nil {
			err = d.SetAutoUpdate(on.On)
		}
	case "set_games":
		var p struct {
			Games []string `json:"games"`
		}
		if err = decode(params, &p); err == nil {
			err = d.SetGames(p.Games)
		}
	case "check_app":
		err = d.CheckAppUpdate(ctx)
	case "update_app":
		err = d.StartAppUpdate()
	case "set_app_auto_update":
		if err = decode(params, &on); err == nil {
			err = d.SetAppAutoUpdate(on.On)
		}
	case "diagnose":
		return d.Diagnose(ctx)
	case "fix":
		var p struct {
			ID string `json:"id"`
		}
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return d.Fix(ctx, p.ID)
	default:
		return nil, fmt.Errorf("неизвестный метод %q", method)
	}
	return d.Status(), err
}

func decode(params json.RawMessage, v any) error {
	if len(params) == 0 {
		return errors.New("нет параметров")
	}
	return json.Unmarshal(params, v)
}
