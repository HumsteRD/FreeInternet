// fi-ctl — команда службе FI из консоли, для отладки: fi-ctl <метод> [параметры JSON].
//
//	fi-ctl status
//	fi-ctl set_strategy {"name":"general (FI STABLE)"}
//
// Печатает ответ службы; у status — только главное, если не задан -full.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"fi/internal/ipc"
)

func main() {
	args := os.Args[1:]
	full := len(args) > 0 && args[0] == "-full"
	if full {
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "fi-ctl [-full] <метод> [параметры JSON]")
		os.Exit(2)
	}
	var params any
	if len(args) > 1 {
		if err := json.Unmarshal([]byte(args[1]), &params); err != nil {
			fmt.Fprintln(os.Stderr, "параметры — не JSON:", err)
			os.Exit(2)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	conn, err := ipc.Dial(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "служба FI не отвечает:", err)
		os.Exit(1)
	}
	defer conn.Close()
	var result map[string]any
	if err := ipc.Call(ctx, conn, args[0], params, &result); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
	if !full {
		// Короткая сводка: в полном ответе — секрет прокси и адрес воркера, в консоль им незачем.
		brief := map[string]any{}
		for _, k := range []string{"enabled", "running", "strategy", "vpn", "anti_cheat", "error", "task", "youtube_via_worker", "via_worker_browsers", "youtube_blocked"} {
			if v, ok := result[k]; ok {
				brief[k] = v
			}
		}
		if services, ok := result["services"].([]any); ok {
			var lines []string
			for _, s := range services {
				if m, ok := s.(map[string]any); ok {
					lines = append(lines, fmt.Sprintf("%v: %v %v", m["title"], m["state"], m["detail"]))
				}
			}
			brief["services"] = lines
		}
		result = brief
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(out))
}
