package singbox

// TunInterfaceName TUN 虚拟网卡名称
const TunInterfaceName = "XrayManagerTun"

// BuildTunConfig 构建 TUN 模式配置。
//
// TUN 进程本身不生成节点出站，而是把接管到的流量经 SOCKS5 转发到目标
// （普通节点 / 链式代理 / 故障转移）已在监听的本地端口——与系统代理语义一致，
// 三种目标通用，也不会与目标进程重复拨号。
//
// bypassProcessPaths 是节点内核自身的可执行文件路径：内核连远端服务器的流量
// 同样会被 TUN 捕获，若再转回本地端口就会成环，所以必须按进程放行直连；
// auto_detect_interface 保证直连出站绑定物理网卡而不是再进 TUN。
func BuildTunConfig(targetPort int, bypassProcessPaths []string) *Config {
	rules := []map[string]interface{}{
		{"action": "sniff"},
		{"protocol": "dns", "action": "hijack-dns"},
	}
	if len(bypassProcessPaths) > 0 {
		rules = append(rules, map[string]interface{}{
			"process_path": bypassProcessPaths,
			"outbound":     "direct",
		})
	}
	rules = append(rules, map[string]interface{}{
		"ip_is_private": true,
		"outbound":      "direct",
	})

	return &Config{
		Log: map[string]interface{}{"level": "warn"},
		Inbounds: []map[string]interface{}{{
			"type":           "tun",
			"tag":            "tun-in",
			"interface_name": TunInterfaceName,
			"address":        []string{"172.19.0.1/30", "fdfe:dcba:9876::1/126"},
			"mtu":            9000,
			"auto_route":     true,
			"strict_route":   true,
			"stack":          "mixed",
		}},
		Outbounds: []map[string]interface{}{
			{
				"type":        "socks",
				"tag":         "proxy",
				"server":      "127.0.0.1",
				"server_port": targetPort,
				"version":     "5",
			},
			{"type": "direct", "tag": "direct"},
		},
		// 远程 DNS 走代理（TCP，避免依赖目标的 UDP 转发），防止 DNS 污染；
		// local 仅用于解析直连出站自身需要的域名
		DNS: map[string]interface{}{
			"servers": []map[string]interface{}{
				{"type": "tcp", "tag": "remote", "server": "1.1.1.1", "detour": "proxy"},
				{"type": "local", "tag": "local"},
			},
			"final": "remote",
		},
		Route: map[string]interface{}{
			"rules":                   rules,
			"final":                   "proxy",
			"auto_detect_interface":   true,
			"default_domain_resolver": "local",
		},
	}
}
