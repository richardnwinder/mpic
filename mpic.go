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

	maxBufSize    = 250 /* common buffer size */
	maxPacketSize = 64  /* max one packet size */

	maxApidxSize  = 0x10 /* v1.4 max number of apidx indexes in the apidx[] array */
	maxApidxLsize = 0x80 /* ver >= 2.0 increased to 128 as number of the apidx indexes */

	maxDcrtSecs14 = 18 /* max dcrt sections v1.4 = 18 */
	maxDcrtSecs20 = 31 /* max dcrt sections v2.0 = 31 */
	maxDcrtSecs21 = 60 /* max dcrt sections v2.1 = 60 */
	maxDcrtSecs30 = 80 /* max dcrt sections v3.0 = 80 */

	maxEcdBsize  = 0x100 /* base encode/decode  buf size */
	maxEcdLsize  = 0x200 /* double buf size */
	maxEcdTbmax  = 0x200 /* default max IN size for tblt (512) */
	maxEcdIbeht  = 0x800 /* EHT max IN size used in bulk_read (2048) */
	maxEcdSbuf14 = 0x400 /* max decoded sbuf size v1.4 */
	maxEcdLbuf14 = 0x700 /* max encoded lbuf size v1.4 */

	maxUsbBsize = 256             /* max txm encode buf size */
	maxUsbLsize = 2 * maxUsbBsize /* long buf size */
	maxUsbDsize = 0x4000          /* 16kb size */
	maxUsbEbuf  = 8192            /* ebuf size */

	ep1in  = 0x00000081
	ep1out = 0x00000001
)

type iobuf struct {
	cnt int
	buf []byte
}

// Device structure
type Device struct {
	dev   *usb.Device
	ver   byte /* used as mp saved verl (12, 14, 20, 21) */
	mtv   byte /* MP version type "4", "5" "6"... as speciied by ver */
	iver  int
	irls  int
	verl  int
	alloc byte /* 0 - no mpic42 allocated 1 - one mp42 device allocated */

	cehwt int /* create EHT timeout (v1.2 -> 600ms, v1.3 -> 450ms) */
	dehwt int /* download EHT timeout (v1.2 -> 500ms, v1.3 -> 300ms) */

	ob  iobuf /* output data buf (EP2) */
	ib  iobuf /* input data buf (EP2) */
	ocb iobuf /* output command buf (EP1) */
	icb iobuf /* input command buf (EP1) (cmd data returned from mpic) */

	acnt  int  /* available count for DECODE OUT (used in encode/decode vers 1.3) */
	sbmax int  /* max short buf data size used in EP2 (dependant on vers) */
	lbmax int  /* max long buf size used in EP2 (dependant on vers) */
	ibeht int  /* max size used for EHT get/set */
	ibrcv int  /* max size used for EP2 IN rcv */
	dcmax int  /* max size used for decode (vers dependant) */
	iderr byte /* decode error flag, 0 - no error, 1 - bad family,  */
	/*  2 - bad EHT, 3 - other error */

	apcsiz int /* current apidx size (v1.4 || ver > 2.0) */

	mdcrt byte /* max dcrt sections version dependant */
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

/******************** sepg_get_vers_mp42 **********************/
/*                                                            */
/* Mir Data Systems 10/02/11                                  */
/*                                                            */
/* Return versin and release numbers.                         */
/**************************************************************/
func (u *Device) sepgGetVersion() (int, int, error) {
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

/********************** sepg_get_set_vers ***********************/
/*		  														*/
/* Mir Data Systems 10/02/11									*/
/*																*/
/* Request and set ivers/irls in the us_g.vers	                */
/* Setup OUT/IN max EP2 buf size used in usb_bulk_read() and    */
/* usb_bulk_write().                                            */
/****************************************************************/
func (u *Device) sepgGetSetVersion() {
	iver, irls, err := u.sepgGetVersion()
	if err != nil { /* on error set default as 1.2 */
		u.iver = 1
		u.irls = 2
	} else {
		u.iver = iver
		u.irls = irls
	}
	u.verl = 10*u.iver + u.irls
	u.ver = byte(u.verl)
	/* setup us_g.sbmax, us_g.lbmax, us_g.ibeht and us_g.dcmax for respective version */
	if u.verl <= 12 {
		u.sbmax = maxUsbBsize   /* used as common short buffer size (0x100 - 256) */
		u.lbmax = maxUsbLsize   /* used as common long  buffer size (0x200 - 512) */
		u.ibeht = maxEcdLsize   /* used as eht buf size    (0x200 - 512) */
		u.ibrcv = maxEcdLsize   /* used as EP2 IN buf size (0x200 - 512) */
		u.dcmax = maxEcdBsize   /* used as decode buf size (0x100 - 256) */
		u.cehwt = 600           /* create EHT timeout in ms */
		u.dehwt = 500           /* download EHT timeout in ms */
		u.apcsiz = maxApidxSize /* current apidx size (0x10) */
		u.mtv = byte('4')       /* new desig */
		u.mdcrt = 0             /* dcrt not used */
	}
	if u.verl >= 13 && u.verl < 20 {
		u.sbmax = maxEcdSbuf14  /* used as common v1.4 short buffer size (0x400 - 1024) */
		u.lbmax = maxEcdLbuf14  /* used as common v1.4 long  buffer size (0x700 - 1792) */
		u.ibeht = maxEcdIbeht   /* used as eht buf size   (0x800 - 2k) */
		u.ibrcv = maxEcdIbeht   /* used as EP2 IN buf size (0x800 - 2k) */
		u.dcmax = maxEcdLbuf14  /* used as decode buf size (0x700 - 1792) */
		u.cehwt = 450           /* create EHT timeout in ms */
		u.dehwt = 370           /* download EHT timeout in ms */
		u.apcsiz = maxApidxSize /* current apidx size (0x10) */
		u.mtv = byte('4')       /* new desig */
		u.mdcrt = maxDcrtSecs14 /* 18 dcrt sections in use  */
	}
	if u.verl >= 20 && u.verl < 30 {
		u.sbmax = maxEcdSbuf14   /* used as default common v2.0 short buffer size */
		u.lbmax = maxEcdLbuf14   /* used as default common v2.0 long  buffer size */
		u.ibeht = maxUsbEbuf     /* used as eht buf size (0x2000 - 8k) */
		u.ibrcv = maxUsbDsize    /* used as EP2 IN buf size (0x4000 - 16k) */
		u.dcmax = maxUsbDsize    /* used as max decode buf size (0x4000 - 16k) */
		u.cehwt = 450            /* create EHT timeout in ms */
		u.dehwt = 370            /* download EHT timeout in ms */
		u.apcsiz = maxApidxLsize /* current apidx size (0x10) */
		u.mtv = byte('5')        /* new desig */
		u.mdcrt = maxDcrtSecs20  /* 31 dcrt sections in use for v20 */
		if u.ver == 21 {
			u.mtv = byte('6')       /* new desig */
			u.mdcrt = maxDcrtSecs21 /* 60 dcrt sections in use for v21 */
		}
	}
	if u.verl >= 30 {
		u.sbmax = maxEcdSbuf14   /* used as default common v3.0 short buffer size */
		u.lbmax = maxEcdLbuf14   /* used as default common v3.0 long  buffer size */
		u.ibeht = maxUsbEbuf     /* used as eht buf size (0x2000 - 8k) */
		u.ibrcv = maxUsbDsize    /* used as EP2 IN buf size (0x4000 - 16k) */
		u.dcmax = maxUsbDsize    /* used as max decode buf size (0x4000 - 16k) */
		u.cehwt = 0              /* create EHT timeout in ms */
		u.dehwt = 0              /* download EHT timeout in ms */
		u.apcsiz = maxApidxLsize /* current apidx size (0x10) */
		u.mtv = byte('7')        /* new desig */
		u.mdcrt = maxDcrtSecs30  /* 80 dcrt sections in use for v30 */
	}
}

// GetVersion function returns version and release number for mpic device
func (u *Device) GetVersion() (int, int, error) {
	iver, irls, err := u.sepgGetVersion()
	return iver, irls, err
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
