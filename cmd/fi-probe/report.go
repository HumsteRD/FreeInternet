package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"fi/internal/probe"
	"fi/internal/selector"
)

var groupTitles = map[string]string{
	"youtube":    "YouTube",
	"discord":    "Discord",
	"cloudflare": "Cloudflare",
	"telegram":   "Telegram",
	"calls":      "Звонки UDP",
	"blocked":    "Заблок. сайты",
	"hosting":    "Хостинги 16КБ",
	"reference":  "Контроль",
}

func groupTitle(g string) string {
	if t, ok := groupTitles[g]; ok {
		return t
	}
	return g
}

// printRun печатает оценки групп одной строкой и неудачные цели под ней.
// Хостингов много, поэтому по ним — только сводка статусов.
func printRun(run selector.Run) {
	if run.Error != "" {
		fmt.Println("  ✗ не запустилась:", run.Error)
		return
	}
	parts := make([]string, 0, len(run.Groups))
	for _, g := range run.Groups {
		parts = append(parts, fmt.Sprintf("%s %d/%d", groupTitle(g.Group), g.Passed, g.Total))
	}
	fmt.Println("  " + strings.Join(parts, " · "))

	hostingStatuses := map[probe.Status]int{}
	for _, r := range run.Results {
		if r.Status == probe.OK {
			continue
		}
		if r.Target.Group == "hosting" {
			hostingStatuses[r.Status]++
			continue
		}
		fmt.Printf("    ✗ %-22s %-12s %s\n", r.Target.Name, r.Status, shorten(r.Detail, 90))
	}
	if len(hostingStatuses) > 0 {
		var s []string
		for st, n := range hostingStatuses {
			s = append(s, fmt.Sprintf("%s×%d", st, n))
		}
		sort.Strings(s)
		fmt.Println("    ✗ хостинги:", strings.Join(s, ", "))
	}
}

func printSummary(runs []selector.Run, dropped []probe.Target) {
	if len(runs) == 0 {
		return
	}
	defer func() {
		if len(dropped) == 0 {
			return
		}
		names := make([]string, len(dropped))
		for i, t := range dropped {
			names[i] = t.Name
		}
		fmt.Printf("\nНе открылись ни в одном прогоне, в оценке не учитываются (%d): %s\n", len(dropped), strings.Join(names, ", "))
	}()
	fmt.Println("\n══ Итог ══")
	tw := tabwriter.NewWriter(os.Stdout, 2, 4, 3, ' ', 0)
	fmt.Fprintln(tw, "  Сервис\tЛучший вариант\tПройдено")
	for _, g := range runs[0].Groups {
		best, score, ok := selector.BestForGroup(runs, g.Group)
		if !ok {
			continue
		}
		fmt.Fprintf(tw, "  %s\t%s\t%d/%d\n", groupTitle(g.Group), best.Strategy, score.Passed, score.Total)
	}
	tw.Flush()

	if best, ok := selector.BestOverall(runs); ok {
		score, max, _ := best.Total()
		fmt.Printf("\nЛучшая в целом: %s (%.1f из %.0f)\n", best.Strategy, score, max)
	}
}

func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
