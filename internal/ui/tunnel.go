package ui

// Бэклог M30/M32: SOCKS/jump-туннель для ВСТРОЕННЫХ RDP/VNC (M33-виджеты).
// Перед запуском клиента, если у соединения задан use proxy (1=SOCKS/3=jump),
// поднимаем туннель, переадресуем цель клиента на 127.0.0.1:<LocalPort> и
// сохраняем *session.Tunnel в tabTunnel[id] (убивается в removeTab).
// applyProxy (cmdline.go) — только ssh-like; здесь — встраиваемые RDP/VNC.

import (
	"strconv"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/session"
)

// инжектируемые границы (юнит-тесты без реального ssh/nc/сокета) — см. tunnel_test.go.
var (
	tunnelAllocPort = session.AllocLocalPort
	tunnelStart     = session.StartTunnel
)

// maybeTunnel — решает, нужен ли туннель, и поднимает его. Возвращает host/port
// для клиента (переадресованные на 127.0.0.1:LocalPort при туннеле) + *Tunnel
// (nil, если туннель не нужен — use proxy=0/путь не задан). Ошибка — туннель
// не удалось поднять (клиент не запускаем).
// uuid/tmpDir — для имён ctl/conf/pipe-файлов (легаси $TMP_DIR/$uuid.ctl и т.п.)
// и password-файла прокси (0600, не в argv — M35). Пустые → tunnel-команда
// соберёт "-F "" -S """ (сломается в StartTunnel-валидации).
func maybeTunnel(env *config.Environment, uuid, tmpDir, host string, port int) (string, int, *session.Tunnel, error) {
	if env == nil {
		return host, port, nil, nil
	}
	// только jump(3)/socks(1); use proxy=0 (defaults) — не связываем для RDP/VNC.
	switch env.UseProxy {
	case 1, 3:
	default:
		return host, port, nil, nil
	}
	if host == "" || port <= 0 {
		return host, port, nil, nil
	}

	localPort, err := tunnelAllocPort()
	if err != nil {
		return host, port, nil, err
	}
	params := session.StartTunnelParams{
		LocalPort:  localPort,
		TargetIP:   host,
		TargetPort: strconv.Itoa(port),
		UseProxy:   int(env.UseProxy),
		UUID:       uuid,
		TmpDir:     tmpDir,
		ProxyIP:    env.ProxyIP,
		ProxyPort:  env.ProxyPort,
		ProxyUser:  env.ProxyUser,
		ProxyPass:  env.ProxyPass,
		JumpIP:     env.JumpIP,
		JumpUser:   env.JumpUser,
		JumpPort:   jumpPortStr(env.JumpPort),
		JumpKey:    env.JumpKey,
	}
	tn, err := tunnelStart(params)
	if err != nil {
		return host, port, nil, err
	}
	if tn == nil {
		return host, port, nil, nil
	}
	return "127.0.0.1", localPort, tn, nil
}

// jumpPortStr — JumpPort (FlexInt) → строка (непустая только при >0).
func jumpPortStr(v config.FlexInt) string {
	if v > 0 {
		return strconv.Itoa(int(v))
	}
	return ""
}
