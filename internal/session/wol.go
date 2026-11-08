package session

// M31: WoL (Wake-on-LAN) — magic packet: 6×0xFF + MAC×16 (102 байта) по UDP :9.
// Современная реализация (net.ParseMAC/net.ListenUDP) вместо Perl-хелперов.

import (
	"fmt"
	"net"
	"syscall"
)

// magicPacket — WoL magic packet из MAC-строки ("00:11:22:33:44:55").
func magicPacket(mac string) ([]byte, error) {
	hw, err := net.ParseMAC(mac)
	if err != nil {
		return nil, err
	}
	if len(hw) != 6 { // M31-ревью #6: EUI-64 (8 байт) молча даёт неверный пакет
		return nil, fmt.Errorf("MAC должен быть 6 байт, got %d", len(hw))
	}
	buf := make([]byte, 6+16*6)
	for i := 0; i < 6; i++ {
		buf[i] = 0xFF
	}
	for i := 0; i < 16; i++ {
		copy(buf[6+i*6:], hw)
	}
	return buf, nil
}

// wolAddr — адрес отправки WoL: указанный IP (или широковещательный), порт 9.
func wolAddr(ip string) string {
	if ip == "" {
		ip = "255.255.255.255"
	}
	return net.JoinHostPort(ip, "9")
}

// setBroadcast — SO_BROADCAST на сокете (ревью M31 #2): без него отправка на
// широковещательный адрес (255.255.255.255) на Linux даёт EACCES/дроп пакета.
func setBroadcast(conn *net.UDPConn) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return
	}
	raw.Control(func(fd uintptr) {
		_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	})
}

// SendWOL — отправляет magic packet (WoL) на ip:9. Если ip пуст — broadcast.
func SendWOL(ip, mac string) error {
	packet, err := magicPacket(mac)
	if err != nil {
		return err
	}
	addr, err := net.ResolveUDPAddr("udp", wolAddr(ip))
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", nil) // unconnected → WriteToUDP
	if err != nil {
		return err
	}
	defer conn.Close()
	setBroadcast(conn)
	_, err = conn.WriteToUDP(packet, addr)
	return err
}
