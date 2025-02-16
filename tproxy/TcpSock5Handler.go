// SPDX-FileCopyrightText: 2022 UnionTech Software Technology Co., Ltd.
//
// SPDX-License-Identifier: GPL-3.0-or-later

package TProxy

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"

	config "github.com/linuxdeepin/deepin-network-proxy/config"
	define "github.com/linuxdeepin/deepin-network-proxy/define"
)

type TcpSock5Handler struct {
	handlerPrv
}

func NewTcpSock5Handler(scope define.Scope, key HandlerKey, proxy config.Proxy, lAddr net.Addr, rAddr net.Addr, lConn net.Conn) *TcpSock5Handler {
	// create new handler
	handler := &TcpSock5Handler{
		handlerPrv: createHandlerPrv(SOCKS5TCP, scope, key, proxy, lAddr, rAddr, lConn),
	}
	// add self to private parent
	handler.saveParent(handler)
	return handler
}

// create tunnel between proxy and server
func (handler *TcpSock5Handler) Tunnel() error {
	// dial proxy server
	rConn, err := handler.dialProxy()
	if err != nil {
		logger.Warningf("[%s] failed to dial proxy server, err: %v", handler.typ, err)
		return err
	}
	// check type
	var port uint16
	var ip net.IP
	dominname := ""
	switch addr := handler.rAddr.(type) {
	case *net.TCPAddr:
		ip = addr.IP
	case *DomainAddr:
		port = uint16(addr.Port)
		ip = net.IPv4(0x00, 0x00, 0x00, 0x01)
		dominname = addr.Domain
	default:
		logger.Warningf("[%s] tunnel addr type is not tcp", handler.typ)
		return errors.New("type is not tcp")
	}
	// auth message
	auth := auth{
		user:     handler.proxy.UserName,
		password: handler.proxy.Password,
	}
	/*
	    sock5 client hand shake request
	  +----+----------+----------+
	  |VER | NMETHODS | METHODS  |
	  +----+----------+----------+
	  | 1  |    1     | 1 to 255 |
	  +----+----------+----------+
	*/
	// sock5 proto
	// buffer := new(bytes.Buffer)
	buf := make([]byte, 3)
	buf[0] = 5
	buf[1] = 1
	buf[2] = 0
	if auth.user != "" && auth.password != "" {
		buf[1] = 2
		buf = append(buf, byte(2))
	}
	// sock5 hand shake
	_, err = rConn.Write(buf)
	if err != nil {
		logger.Warningf("[%s] hand shake request failed, err: %v", handler.typ, err)
		return err
	}
	/*
		sock5 server hand shake response
		+----+--------+
		|VER | METHOD |
		+----+--------+
		| 1  |   1    |
		+----+--------+
	*/
	_, err = rConn.Read(buf)
	if err != nil {
		logger.Warningf("[%s] hand shake response failed, err: %v", handler.typ, err)
		return err
	}
	logger.Debugf("[%s] hand shake response success message auth method: %v", handler.typ, buf[1])
	if buf[0] != 5 || (buf[1] != 0 && buf[1] != 2) {
		return fmt.Errorf("sock5 proto is invalid, sock type: %v, method: %v", buf[0], buf[1])
	}
	// check if server need auth
	if buf[1] == 2 {
		logger.Debugf("[%s] proxy need auth, start authenticating...", handler.typ)
		/*
		    sock5 auth request
		  +----+------+----------+------+----------+
		  |VER | ULEN |  UNAME   | PLEN |  PASSWD  |
		  +----+------+----------+------+----------+
		  | 1  |  1   | 1 to 255 |  1   | 1 to 255 |
		  +----+------+----------+------+----------+
		*/
		buf = make([]byte, 1)
		buf[0] = 1
		buf = append(buf, byte(len(auth.user)))
		buf = append(buf, []byte(auth.user)...)
		buf = append(buf, byte(len(auth.password)))
		buf = append(buf, []byte(auth.password)...)
		// write auth message to writer
		_, err = rConn.Write(buf)
		if err != nil {
			logger.Warningf("[%s] auth request failed, err: %v", handler.typ, err)
			return err
		}
		buf = make([]byte, 32)
		_, err = rConn.Read(buf)
		if err != nil {
			logger.Warningf("[%s] auth response failed, err: %v", handler.typ, err)
			return err
		}
		// RFC1929 user/pass auth should return 1, but some sock5 return 5
		if buf[0] != 5 && buf[0] != 1 {
			logger.Warningf("[%s] auth response incorrect code, code: %v", handler.typ, buf[0])
			return fmt.Errorf("incorrect sock5 auth response, code: %v", buf[0])
		}
		logger.Debugf("[%s] auth success, code: %v", handler.typ, buf[0])
	}
	/*
			sock5 connect request
		   +----+-----+-------+------+----------+----------+
		   |VER | CMD |  RSV  | ATYP | DST.ADDR | DST.PORT |
		   +----+-----+-------+------+----------+----------+
		   | 1  |  1  | X'00' |  1   | Variable |    2     |
		   +----+-----+-------+------+----------+----------+
	*/
	// start create tunnel
	buf = make([]byte, 4)
	buf[0] = 5
	buf[1] = 1 // connect
	buf[2] = 0 // reserved
	// add tcpAddr
	if dominname == "" {
		if len(ip) == net.IPv4len && ip.To4() != nil {
			buf[3] = 1
			buf = append(buf, ip.To4()...)
		} else if ip.To16() != nil {
			buf[3] = 4
			buf = append(buf, ip.To16()...)
		} else {
			return errors.New("ip invalid")
		}
	} else {
		if len(dominname) > 255 {
			return errors.New("domain name out of max length")
		}
		buf[3] = 3
		buf = append(buf, byte(len(dominname)))
		buf = append(buf, []byte(dominname)...)
	}
	// convert port 2 byte
	if port == 0 {
		port = 80
	}
	portByte := make([]byte, 2)
	binary.BigEndian.PutUint16(portByte, port)
	buf = append(buf, portByte...)
	// request proxy connect rConn server
	logger.Debugf("[%s] send connect request, buf: %v", handler.typ, buf)
	_, err = rConn.Write(buf)
	if err != nil {
		logger.Warningf("[%s] send connect request failed, err: %v", handler.typ, err)
		return err
	}
	logger.Debugf("[%s] request successfully", handler.typ)

	// resp
	// VER REP RSV
	_, err = io.ReadFull(rConn, buf[0:3])
	if err != nil {
		logger.Warningf("[%s] connect response failed, err: %v", handler.typ, err)
		return err
	}
	if buf[0] != 5 || buf[1] != 0 {
		logger.Warningf("[%s] connect response failed, version: %v, code: %v", handler.typ, buf[0], buf[1])
		return fmt.Errorf("incorrect sock5 connect reponse, version: %v, code: %v", buf[0], buf[1])
	}

	// ATYPE
	_, err = io.ReadFull(rConn, buf[0:1])
	if err != nil {
		logger.Warningf("[%s] connect response failed, err: %v", handler.typ, err)
		return err
	}

	// IP
	var addrLen int
	switch buf[0] {
	case 1:
		addrLen = 4
	case 4:
		addrLen = 16
	case 3:
		_, err = io.ReadFull(rConn, buf[0:1])
		if err != nil {
			logger.Warningf("[%s] connect response failed, err: %v", handler.typ, err)
			return err
		}
		addrLen = int(buf[0])
	default:
		return errors.New("invalid ip")
	}

	if len(buf) < addrLen {
		buf = make([]byte, addrLen)
	}

	_, err = io.ReadFull(rConn, buf[0:addrLen])
	if err != nil {
		logger.Warningf("[%s] connect response failed, err: %v", handler.typ, err)
		return err
	}

	// PORT
	_, err = io.ReadFull(rConn, buf[0:2])
	if err != nil {
		logger.Warningf("[%s] connect response failed, err: %v", handler.typ, err)
		return err
	}

	logger.Debugf("[%s] proxy: tunnel create success, [%s] -> [%s] -> [%s]",
		handler.typ, handler.lAddr.String(), rConn.RemoteAddr(), handler.rAddr.String())
	// save rConn handler
	handler.rConn = rConn
	return nil
}
