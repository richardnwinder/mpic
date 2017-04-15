package mpic

import (
	"errors"
	"fmt"
	"time"

	"github.com/richardnwinder/usb"
)

const (
	mp42Vid = 0x04d8 /* mp42 VID (Mchip) */
	mp42Pid = 0xfca7 /* mp42 PID (MDS license) */

	maxBufSize    = 250   /* common buffer size */
	maxPacketSize = 64    /* max one packet size */
	maxEcdIbeht   = 0x800 /* EHT max IN size used in bulk_read (2048) */

	ep1in  = 0x00000081
	ep1out = 0x00000001
)

type iobuf struct {
	cnt int
	buf []byte
}

// Device structure
type Device struct {
	dev *usb.Device
	ob  iobuf /* output data buf (EP2) */
	ib  iobuf /* input data buf (EP2) */
	ocb iobuf /* output command buf (EP1) */
	icb iobuf /* input command buf (EP1) (cmd data returned from mpic) */
	/* handles to use for EP1 and EP2 */
	ep1 uint32
	ep2 uint32
}

func resetBuffer(ibuf []byte, ilen int) {
	for icnt := 0; icnt < ilen; icnt++ {
		ibuf[icnt] = 0x00
	}
}

// Open function connects mpic device
func Open() (*Device, error) {
	var err error
	device, err := usb.OpenVidPid(mp42Vid, mp42Pid)
	if err != nil {
		return nil, err
	}
	mpic := &Device{
		dev: device,
		ob:  iobuf{cnt: 0, buf: make([]byte, maxEcdIbeht)},
		ib:  iobuf{cnt: 0, buf: make([]byte, maxEcdIbeht)},
		ocb: iobuf{cnt: 0, buf: make([]byte, maxEcdIbeht)},
		icb: iobuf{cnt: 0, buf: make([]byte, maxEcdIbeht)},
		ep1: 1,
		ep2: 3,
	}
	return mpic, nil
}

// Close function disconnects mpic device
func (u *Device) Close() {
	u.dev.Close()
}

// ClaimInterface function connects mpic device interface
func (u *Device) ClaimInterface(n uint32) error {
	e := u.dev.ClaimInterface(n)
	return e
}

// ReleaseInterface function disconnects mpic device interface
func (u *Device) ReleaseInterface(n uint32) error {
	e := u.dev.ReleaseInterface(n)
	return e
}

func (u *Device) sepgGetInsync(endpoint uint32) error {
	var timeout uint32 = 3000
	var cdata []byte
	cdata = make([]byte, maxBufSize)
	//var odata []byte
	//odata = make([]byte, maxBufSize)
	idcnt, _, err := u.dev.BulkTransfer(endpoint, 1, timeout, cdata)
	if err != nil {
		return err
	}
	if (idcnt != 1) || (cdata[0] != byte(0xff)) {
		return errors.New("USB insync error")
	}
	return nil
}

func (u *Device) sepgCmdExec(cmd byte, ccnt int, cbuf []byte) (int, []byte, error) {
	var timeout = 1000
	/*-- send command ---*/
	idcnt, _, err := u.dev.BulkTransfer(ep1out, uint32(ccnt), uint32(timeout), cbuf)
	if err != nil {
		return 0, nil, err
	}
	if idcnt != ccnt {
		return 0, nil, errors.New("Can not send USB command!")
	}
	/* if IN command pending */
	if (cmd & 0x80) != 0 {

		err := u.sepgGetInsync(ep1in) // get INSYNC on EP1 */
		if err != nil {
			fmt.Println(err)
			return 0, nil, errors.New("Bad INSYNC on EP1!")
		}
		var cdata []byte
		cdata = make([]byte, maxBufSize)

		time.Sleep(60) // Wait until mp2 data fixed for IN request (get details)
		idcnt, odata, err := u.dev.BulkTransfer(ep1in, uint32(maxPacketSize), uint32(timeout), cdata)
		if err != nil {
			return 0, nil, err
		}
		return idcnt, odata, nil
	}
	return 0, nil, nil
}

// Each command starts with 3 bytes
// w0 - dest  - destination, 4 - mp4x
// w1 - cmd   - command specification (0 - 0xff)
// w2 - ccnt  - command byte counter  (0 - 0x3c)
//
// comand_data (if ccnt != 0) follows:
// ccb[ccnt]  - command data (icnt <= max_packet_size - 2)
//              ccnt_max = 60 (0x3c)
// icb[inct]  - returned command data (if any) (max 64 words)
//
// Two command types are defined:
// OCMD = OUT command (cmd, b7 = 0)
//
// ICMD = IN command (cmd, b7 = 1)
//           command with following INSYNG and data IN if any
//																*/
// OCMD and ICMD are send via EP1 (endpoint 1)
func (u *Device) sepgCmd(dest byte, cmd byte, ccnt byte, ccb []byte) (int, []byte, error) {
	//fmt.Printf("dest : %d\n", dest)
	//fmt.Printf("cmd : %d\n", cmd)
	//fmt.Printf("ccnt : %d\n", ccnt)
	//fmt.Printf("len(ccb) : %d\n", len(ccb))
	var cp []byte
	cp = make([]byte, maxBufSize)
	cp[0] = dest
	cp[1] = cmd
	cp[2] = ccnt
	var cnt = 3
	for icnt := 0; icnt < int(ccnt); icnt++ {
		cp[cnt] = ccb[icnt]
		cnt++
	}
	icnt, icb, err := u.sepgCmdExec(cmd, cnt, cp) // execute command
	return icnt, icb, err
}

// GetVersion function returns version and release number for mpic device
func (u *Device) GetVersion() (int, int, error) {
	var mobuf []byte
	mobuf = make([]byte, maxBufSize)
	micnt, mibuf, err := u.sepgCmd(4, 0x93, 0, mobuf)
	if err != nil {
		return 0, 0, err
	}
	if micnt != 2 {
		return 0, 0, errors.New("Bad Response")
	}
	iver := int(mibuf[0])
	irls := int(mibuf[1])
	return iver, irls, nil
}

// Activate function returns active flag
func (u *Device) Activate() (int, int, error) {
	//if()
	var mobuf []byte
	mobuf = make([]byte, maxBufSize)
	micnt, mibuf, err := u.sepgCmd(4, 0x93, 0, mobuf)
	if err != nil {
		return 0, 0, err
	}
	if micnt != 2 {
		return 0, 0, errors.New("Bad Response")
	}
	iver := int(mibuf[0])
	irls := int(mibuf[1])
	return iver, irls, nil
}
