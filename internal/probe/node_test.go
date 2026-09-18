package probe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNodeHost(t *testing.T) {
	// Пары «кластер — узел»: из обратных DNS-записей узлов, из report_mapping, подтверждённого
	// DNS (ams17s15), и узел провайдера PLDT в Маниле (адрес 202.138.162.12).
	pairs := map[string]string{
		"svo04s41":   "n8v7znze",
		"lga34s48":   "ab5sznzr",
		"dfw25s54":   "q4fl6n6z",
		"syd09s21":   "ntq7ynle",
		"lga25s82":   "ab5l6nrl",
		"ams17s15":   "5hnekne6",
		"pldt-mnl31": "2aqu-hoase",
	}
	for cluster, code := range pairs {
		host, ok := NodeHost(cluster)
		if want := "rr1---sn-" + code + ".googlevideo.com"; !ok || host != want {
			t.Errorf("NodeHost(%q) = %q, %v; want %q", cluster, host, ok, want)
		}
	}
	for _, bad := range []string{"", "AMS17", "ams 17", "ams.17"} {
		if _, ok := NodeHost(bad); ok {
			t.Errorf("NodeHost(%q) принял неверное имя", bad)
		}
	}
}

func TestFindYouTubeNode(t *testing.T) {
	answer := ""
	var answers []string // если заданы — отдаются по очереди
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if len(answers) > 0 {
			answer, answers = answers[0], answers[1:]
		}
		w.Write([]byte(answer))
	}))
	defer srv.Close()
	oldURL, oldLookup := reportMappingURL, lookupHost
	defer func() { reportMappingURL, lookupHost = oldURL, oldLookup }()
	reportMappingURL = srv.URL

	known := map[string]bool{"rr1---sn-n8v7znze.googlevideo.com": true}
	lookupHost = func(_ context.Context, host string) ([]string, error) {
		if known[host] {
			return []string{"192.0.2.1"}, nil
		}
		return nil, errors.New("no such host")
	}

	answer = "203.0.113.5 => svo04s41 : router: \t \"pf01.svo04\" next_hop_address: \"192.0.2.9\" (203.0.113.0/24)\n"
	host, err := FindYouTubeNode(context.Background(), srv.Client())
	if err != nil || host != "rr1---sn-n8v7znze.googlevideo.com" {
		t.Fatalf("FindYouTubeNode = %q, %v", host, err)
	}

	// Транзитная зона вместо кластера: узла в DNS нет, redirector спрашивается снова.
	zone := "203.0.113.5 => tzfraa-ab : router: \"pf03.fra15\"\n"
	answers, requests = []string{zone, zone, answer}, 0
	if host, err := FindYouTubeNode(context.Background(), srv.Client()); err != nil || host != "rr1---sn-n8v7znze.googlevideo.com" || requests != 3 {
		t.Fatalf("после транзитной зоны: %q, %v, запросов %d", host, err, requests)
	}

	answer, requests = zone, 0
	if host, err := FindYouTubeNode(context.Background(), srv.Client()); err == nil {
		t.Fatalf("принят узел, которого нет в DNS: %q", host)
	}
	if requests != nodeAttempts {
		t.Fatalf("запросов %d, ждали %d", requests, nodeAttempts)
	}

	answer, requests = "<html>что-то другое</html>", 0
	if _, err := FindYouTubeNode(context.Background(), srv.Client()); err == nil || requests != 1 {
		t.Fatalf("непонятный ответ: %v, запросов %d", err, requests)
	}
}

func TestWithYouTubeNode(t *testing.T) {
	base := DefaultTargets()
	if got := WithYouTubeNode(base, ""); len(got) != len(base) || got[2].Host != videoRedirector {
		t.Fatal("без узла цели должны остаться прежними")
	}
	got := WithYouTubeNode(base, "rr1---sn-n8v7znze.googlevideo.com")
	found := false
	for _, target := range got {
		if target.Host == videoRedirector {
			t.Fatal("redirector остался в целях")
		}
		if target.Host == "rr1---sn-n8v7znze.googlevideo.com" {
			found = target.Group == "youtube" && target.Kind == KindUpload
		}
	}
	if !found {
		t.Fatal("проверка узла сети не добавлена")
	}
	for _, target := range base {
		if target.Host != videoRedirector && target.Name == "YouTube: видеосервер вашей сети" {
			t.Fatal("WithYouTubeNode изменил исходный срез")
		}
	}
}
