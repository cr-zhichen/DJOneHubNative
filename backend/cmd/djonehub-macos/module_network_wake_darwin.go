//go:build darwin && cgo

package main

import (
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"
)

//go:embed module_network_wake.sh
var moduleNetworkWakeScript []byte

//go:embed module_network_wake_init.sh
var moduleNetworkWakeInitScript []byte

const (
	moduleNetworkWakeBinary = "/data/djonehub/bin/djonehub-network-wake"
	moduleNetworkWakeInit   = "/etc/init.d/djonehub_network_wake"
	moduleNetworkWakeLink   = "/etc/rc5.d/S98djonehub_network_wake"
	moduleNetworkWakeFlag   = "/data/djonehub/network-wake.enabled"
)

func enableModuleNetworkWake() error {
	adb, err := openDJIUSBADB()
	if err != nil {
		return fmt.Errorf("打开模块 ADB 失败: %w", err)
	}
	defer adb.Close()
	if err := requireModuleRoot(adb); err != nil {
		return err
	}
	if err := adb.push(moduleNetworkWakeScript, "/data/local/tmp/djonehub-network-wake.new", 0o100755, 15*time.Second); err != nil {
		return fmt.Errorf("上传网络唤醒服务失败: %w", err)
	}
	if err := adb.push(moduleNetworkWakeInitScript, "/data/local/tmp/djonehub_network_wake.new", 0o100755, 15*time.Second); err != nil {
		return fmt.Errorf("上传网络唤醒启动项失败: %w", err)
	}
	// QDC507 固件的根文件系统（含 /etc）默认只读挂载，必须先 remount 读写才能
	// 安装 init.d 自启动项；装完恢复只读。不要用 set -e：任何一步失败都会
	// 让模块 shell 静默退出、拿不到退出状态（表现为"没有返回退出状态"），
	// 改为每步失败打日志、由最后的 status 退出码统一判定。
	command := "mkdir -p /data/djonehub/bin /data/djonehub/log /data/djonehub/backup; " +
		"stamp=$(date +%Y%m%d-%H%M%S); backup=/data/djonehub/backup/network-wake-$stamp; mkdir -p \"$backup\"; " +
		"test ! -e " + moduleNetworkWakeBinary + " || cp -p " + moduleNetworkWakeBinary + " \"$backup/\"; " +
		"mounted=0; if mount -o remount,rw / 2>/dev/null; then mounted=1; else echo 'wake: 根文件系统重挂载为读写失败'; fi; " +
		"cp /data/local/tmp/djonehub-network-wake.new " + moduleNetworkWakeBinary + " && chmod 755 " + moduleNetworkWakeBinary + " || echo 'wake: 写入守护脚本失败'; " +
		"cp /data/local/tmp/djonehub_network_wake.new " + moduleNetworkWakeInit + " && chmod 755 " + moduleNetworkWakeInit + " || echo 'wake: 写入 init 脚本失败'; " +
		"ln -sfn ../init.d/djonehub_network_wake " + moduleNetworkWakeLink + " || echo 'wake: 创建自启动链接失败'; " +
		"if [ \"$mounted\" = 1 ]; then sync; mount -o remount,ro / 2>/dev/null || true; fi; " +
		"touch " + moduleNetworkWakeFlag + "; " +
		moduleNetworkWakeInit + " restart; echo wake_restart_rc=$?; " +
		moduleNetworkWakeInit + " status"
	out, status, err := adb.shellChecked(command, 30*time.Second)
	if err != nil {
		return fmt.Errorf("安装网络唤醒服务失败: %w", err)
	}
	if status != 0 {
		return fmt.Errorf("网络唤醒服务未启动（status=%d, output=%s）", status, strings.TrimSpace(out))
	}
	return nil
}

func disableModuleNetworkWake() error {
	adb, err := openDJIUSBADB()
	if err != nil {
		return err
	}
	defer adb.Close()
	if err := requireModuleRoot(adb); err != nil {
		return err
	}
	out, status, err := adb.shellChecked("rm -f "+moduleNetworkWakeFlag+"; test ! -x "+moduleNetworkWakeInit+" || "+moduleNetworkWakeInit+" stop", 12*time.Second)
	if err != nil {
		return err
	}
	if status != 0 {
		return fmt.Errorf("停用网络唤醒服务失败（status=%d, output=%s）", status, strings.TrimSpace(out))
	}
	return nil
}

func requireModuleRoot(adb *adbClient) error {
	out, status, err := adb.shellChecked("id -u", 8*time.Second)
	if err != nil {
		return err
	}
	isRoot := false
	for _, field := range strings.Fields(out) {
		if field == "0" {
			isRoot = true
			break
		}
	}
	if status != 0 || !isRoot {
		return errors.New("模块 ADB 没有 root 权限，无法安装网络唤醒服务")
	}
	return nil
}
