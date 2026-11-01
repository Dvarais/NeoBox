//go:build windows

package security

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows Filtering Platform (WFP) Native Subsystem
//
// Implements an ephemeral, kernel-level Kill Switch using fwpuclnt.dll.
// Unlike netsh advfirewall which modifies the persistent registry database,
// WFP dynamic sessions (FWPM_SESSION_FLAG_DYNAMIC) are tied by the kernel
// to the RPC handle of this process: if NeoBox crashes, is killed, or shuts down,
// the Windows kernel automatically and instantly tears down all filters.

var (
	modfwpuclnt = windows.NewLazySystemDLL("fwpuclnt.dll")
	modiphlpapi = windows.NewLazySystemDLL("iphlpapi.dll")

	procFwpmEngineOpen0           = modfwpuclnt.NewProc("FwpmEngineOpen0")
	procFwpmEngineClose0          = modfwpuclnt.NewProc("FwpmEngineClose0")
	procFwpmTransactionBegin0     = modfwpuclnt.NewProc("FwpmTransactionBegin0")
	procFwpmTransactionCommit0    = modfwpuclnt.NewProc("FwpmTransactionCommit0")
	procFwpmTransactionAbort0     = modfwpuclnt.NewProc("FwpmTransactionAbort0")
	procFwpmProviderAdd0          = modfwpuclnt.NewProc("FwpmProviderAdd0")
	procFwpmSubLayerAdd0          = modfwpuclnt.NewProc("FwpmSubLayerAdd0")
	procFwpmFilterAdd0            = modfwpuclnt.NewProc("FwpmFilterAdd0")
	procFwpmGetAppIdFromFileName0 = modfwpuclnt.NewProc("FwpmGetAppIdFromFileName0")
	procFwpmFreeMemory0           = modfwpuclnt.NewProc("FwpmFreeMemory0")

	procConvertInterfaceIndexToLuid = modiphlpapi.NewProc("ConvertInterfaceIndexToLuid")
)

const (
	rpcCAUthNWinNT         = 10
	fwpmSessionFlagDynamic = 0x00000001

	fwpActionBlock  = 0x00000001 | 0x00001000 // FWP_ACTION_FLAG_TERMINATING
	fwpActionPermit = 0x00000002 | 0x00001000

	fwpMatchEqual       = 0
	fwpMatchFlagsAnySet = 7

	fwpUint32        = 3
	fwpUint64        = 4
	fwpByteBlobType  = 12

	fwpConditionFlagIsLoopback = 0x00000001
)

// WFP ALE Layers and Conditions
var (
	layerALEAuthConnectV4 = windows.GUID{Data1: 0xc38d57d1, Data2: 0x05a7, Data3: 0x4c33, Data4: [8]byte{0x90, 0x4f, 0x7f, 0xb4, 0xe7, 0x3e, 0x67, 0x66}}
	layerALEAuthConnectV6 = windows.GUID{Data1: 0x4a72393b, Data2: 0x319f, Data3: 0x4e11, Data4: [8]byte{0xbc, 0x44, 0xa0, 0x3d, 0x0f, 0x21, 0x50, 0xc3}}
	layerALEAuthRecvV4    = windows.GUID{Data1: 0xe1cd6407, Data2: 0x652a, Data3: 0x436d, Data4: [8]byte{0x9d, 0x41, 0xbf, 0x41, 0xec, 0x1b, 0x4e, 0x47}}
	layerALEAuthRecvV6    = windows.GUID{Data1: 0xa135f661, Data2: 0x0b5c, Data3: 0x482a, Data4: [8]byte{0x87, 0x42, 0xf0, 0x4b, 0xf4, 0x91, 0xe7, 0x79}}

	condALEAppID       = windows.GUID{Data1: 0xd78e1e87, Data2: 0x8644, Data3: 0x4ea5, Data4: [8]byte{0x94, 0x37, 0xd8, 0x09, 0x87, 0x8e, 0x40, 0xbc}}
	condRemoteAddress  = windows.GUID{Data1: 0xb23a2737, Data2: 0xf9a3, Data3: 0x4a9c, Data4: [8]byte{0x90, 0x8d, 0x3d, 0x3e, 0xfa, 0x11, 0x07, 0x24}}
	condLocalInterface = windows.GUID{Data1: 0x4cd62a49, Data2: 0x59c3, Data3: 0x4969, Data4: [8]byte{0xb7, 0xf3, 0xbd, 0xa5, 0xd3, 0x28, 0x90, 0xa4}}
	condFlags          = windows.GUID{Data1: 0xe04fb139, Data2: 0x446a, Data3: 0x48a0, Data4: [8]byte{0x97, 0x4a, 0x47, 0x1a, 0xe4, 0xe8, 0x4b, 0x72}}
)

type fwpmDisplayData0 struct {
	name        *uint16
	description *uint16
}

type fwpmSession0 struct {
	sessionKey           windows.GUID
	displayData          fwpmDisplayData0
	flags                uint32
	txnWaitTimeoutInMSec uint32
	processId            uint32
	_                    uint32 // 4-byte padding for 8-byte pointer alignment
	sid                  *windows.SID
	username             *uint16
	kernelMode           uint32
	_                    uint32 // 4-byte struct tail padding
}

type fwpByteBlob struct {
	size uint32
	_    uint32
	data *uint8
}

type fwpmProvider0 struct {
	providerKey  windows.GUID
	displayData  fwpmDisplayData0
	flags        uint32
	_            uint32
	providerData fwpByteBlob
	serviceName  *uint16
}

type fwpmSublayer0 struct {
	subLayerKey  windows.GUID
	displayData  fwpmDisplayData0
	flags        uint32
	_            uint32
	providerKey  *windows.GUID
	providerData fwpByteBlob
	weight       uint16
	_            [6]byte
}

type fwpValue0 struct {
	valType uint32
	_       [4]byte
	value   uintptr
}

type fwpConditionValue0 fwpValue0

type fwpmFilterCondition0 struct {
	fieldKey       windows.GUID
	matchType      uint32
	_              [4]byte
	conditionValue fwpConditionValue0
}

type fwpmAction0 struct {
	actionType uint32
	filterType windows.GUID
}

type fwpmFilter0 struct {
	filterKey           windows.GUID
	displayData         fwpmDisplayData0
	flags               uint32
	_                   uint32
	providerKey         *windows.GUID
	providerData        fwpByteBlob
	layerKey            windows.GUID
	subLayerKey         windows.GUID
	weight              fwpValue0
	numFilterConditions uint32
	_                   uint32
	filterCondition     *fwpmFilterCondition0
	action              fwpmAction0
	offset1             [4]byte
	providerContextKey  windows.GUID
	reserved            *windows.GUID
	filterID            uint64
	effectiveWeight     fwpValue0
}

var (
	wfpMu           sync.Mutex
	activeWFPEngine uintptr
)

// IsWFPActive reports whether a dynamic WFP kernel session is currently active.
func IsWFPActive() bool {
	wfpMu.Lock()
	defer wfpMu.Unlock()
	return activeWFPEngine != 0
}

func abortAndCloseWFP(engine uintptr) {
	_, _, _ = procFwpmTransactionAbort0.Call(engine)
	_, _, _ = procFwpmEngineClose0.Call(engine)
}

// EnableWFPKillSwitch arms a dynamic kernel WFP session with fail-safe zero-persisted state.
func EnableWFPKillSwitch(serverIPs []net.IP, tunIfName string) error {
	wfpMu.Lock()
	defer wfpMu.Unlock()

	if activeWFPEngine != 0 {
		_ = closeWFP(activeWFPEngine)
		activeWFPEngine = 0
	}

	nameUTF16, err := windows.UTF16PtrFromString("NeoBox Dynamic Kill Switch")
	if err != nil {
		return err
	}
	descUTF16, _ := windows.UTF16PtrFromString("Ephemeral WFP Kill Switch Session")

	session := fwpmSession0{
		displayData: fwpmDisplayData0{
			name:        nameUTF16,
			description: descUTF16,
		},
		flags:                fwpmSessionFlagDynamic,
		txnWaitTimeoutInMSec: windows.INFINITE,
	}

	var engineHandle uintptr
	r1, _, callErr := procFwpmEngineOpen0.Call(0, rpcCAUthNWinNT, 0, uintptr(unsafe.Pointer(&session)), uintptr(unsafe.Pointer(&engineHandle)))
	if r1 != 0 {
		return fmt.Errorf("FwpmEngineOpen0 failed: %w (code 0x%x)", callErr, r1)
	}

	// Begin atomic transaction
	if r1, _, callErr := procFwpmTransactionBegin0.Call(engineHandle, 0); r1 != 0 {
		_, _, _ = procFwpmEngineClose0.Call(engineHandle)
		return fmt.Errorf("FwpmTransactionBegin0 failed: %w", callErr)
	}

	providerGUID, err := windows.GenerateGUID()
	if err != nil {
		abortAndCloseWFP(engineHandle)
		return err
	}
	sublayerGUID, err := windows.GenerateGUID()
	if err != nil {
		abortAndCloseWFP(engineHandle)
		return err
	}

	// Register Provider
	provider := fwpmProvider0{
		providerKey: providerGUID,
		displayData: fwpmDisplayData0{name: nameUTF16},
	}
	if r1, _, callErr := procFwpmProviderAdd0.Call(engineHandle, uintptr(unsafe.Pointer(&provider)), 0); r1 != 0 {
		abortAndCloseWFP(engineHandle)
		return fmt.Errorf("FwpmProviderAdd0 failed: %w", callErr)
	}

	// Register SubLayer with maximum weight (0xFFFF) so Kill Switch rules evaluate before standard firewall
	sublayer := fwpmSublayer0{
		subLayerKey: sublayerGUID,
		displayData: fwpmDisplayData0{name: nameUTF16},
		providerKey: &providerGUID,
		weight:      0xFFFF,
	}
	if r1, _, callErr := procFwpmSubLayerAdd0.Call(engineHandle, uintptr(unsafe.Pointer(&sublayer)), 0); r1 != 0 {
		abortAndCloseWFP(engineHandle)
		return fmt.Errorf("FwpmSubLayerAdd0 failed: %w", callErr)
	}

	// Permit Loopback (weight 15)
	if err := addPermitLoopback(engineHandle, providerGUID, sublayerGUID, 15); err != nil {
		abortAndCloseWFP(engineHandle)
		return fmt.Errorf("addPermitLoopback failed: %w", err)
	}

	// Permit current executable (NeoBox app & sing-box runtime) (weight 14)
	if exePath, err := os.Executable(); err == nil && exePath != "" {
		_ = addPermitAppFilter(engineHandle, providerGUID, sublayerGUID, 14, exePath)
	}

	// Permit direct connections to VPN server IPs (weight 13)
	for _, ip := range serverIPs {
		if ip4 := ip.To4(); ip4 != nil {
			_ = addPermitServerIP4Filter(engineHandle, providerGUID, sublayerGUID, 13, ip4)
		}
	}

	// Permit TUN Adapter (weight 12) if active.
	// If TUN interface was specified but cannot be resolved or bound to LUID,
	// abort the transaction so Tier-2 Netsh fallback can safely handle it.
	if tunIfName != "" {
		iface, err := net.InterfaceByName(tunIfName)
		if err != nil {
			abortAndCloseWFP(engineHandle)
			return fmt.Errorf("lookup TUN interface %q failed: %w", tunIfName, err)
		}
		var tunLUID uint64
		r1, _, _ := procConvertInterfaceIndexToLuid.Call(uintptr(iface.Index), uintptr(unsafe.Pointer(&tunLUID)))
		if r1 != 0 || tunLUID == 0 {
			abortAndCloseWFP(engineHandle)
			return fmt.Errorf("convert interface index to LUID for %q failed (code %d)", tunIfName, r1)
		}
		if err := addPermitTunFilter(engineHandle, providerGUID, sublayerGUID, 12, tunLUID); err != nil {
			abortAndCloseWFP(engineHandle)
			return fmt.Errorf("addPermitTunFilter failed: %w", err)
		}
	}

	// Add Block-All outbound terminating filter (weight 0)
	if err := addBlockAllFilter(engineHandle, providerGUID, sublayerGUID); err != nil {
		abortAndCloseWFP(engineHandle)
		return fmt.Errorf("addBlockAllFilter failed: %w", err)
	}

	// Commit atomic transaction
	if r1, _, callErr := procFwpmTransactionCommit0.Call(engineHandle); r1 != 0 {
		abortAndCloseWFP(engineHandle)
		return fmt.Errorf("FwpmTransactionCommit0 failed: %w", callErr)
	}

	activeWFPEngine = engineHandle
	return nil
}

// DisableWFPKillSwitch tears down the active WFP session, unregistering all filters.
func DisableWFPKillSwitch() error {
	wfpMu.Lock()
	defer wfpMu.Unlock()

	if activeWFPEngine == 0 {
		return nil
	}
	err := closeWFP(activeWFPEngine)
	activeWFPEngine = 0
	return err
}

func closeWFP(engine uintptr) error {
	r1, _, err := procFwpmEngineClose0.Call(engine)
	if r1 != 0 {
		return fmt.Errorf("FwpmEngineClose0 failed: %w (code 0x%x)", err, r1)
	}
	return nil
}

func addPermitLoopback(engine uintptr, provider, sublayer windows.GUID, weight uint8) error {
	flag := uint32(fwpConditionFlagIsLoopback)
	cond := fwpmFilterCondition0{
		fieldKey:  condFlags,
		matchType: fwpMatchFlagsAnySet,
		conditionValue: fwpConditionValue0{
			valType: fwpUint32,
			value:   uintptr(flag),
		},
	}

	name, _ := windows.UTF16PtrFromString("Permit Loopback")
	layers := []windows.GUID{layerALEAuthConnectV4, layerALEAuthConnectV6}
	for _, layer := range layers {
		filter := fwpmFilter0{
			displayData:         fwpmDisplayData0{name: name},
			providerKey:         &provider,
			layerKey:            layer,
			subLayerKey:         sublayer,
			weight:              fwpValue0{valType: fwpUint32, value: uintptr(weight)},
			numFilterConditions: 1,
			filterCondition:     &cond,
			action:              fwpmAction0{actionType: fwpActionPermit},
		}
		var filterID uint64
		r1, _, err := procFwpmFilterAdd0.Call(engine, uintptr(unsafe.Pointer(&filter)), 0, uintptr(unsafe.Pointer(&filterID)))
		if r1 != 0 {
			return err
		}
	}
	return nil
}

func addPermitAppFilter(engine uintptr, provider, sublayer windows.GUID, weight uint8, exePath string) error {
	if procFwpmGetAppIdFromFileName0.Find() != nil {
		return nil
	}
	path16, err := windows.UTF16PtrFromString(exePath)
	if err != nil {
		return err
	}
	var appBlob *fwpByteBlob
	r1, _, err := procFwpmGetAppIdFromFileName0.Call(uintptr(unsafe.Pointer(path16)), uintptr(unsafe.Pointer(&appBlob)))
	if r1 != 0 || appBlob == nil {
		return err
	}
	defer func() {
		if procFwpmFreeMemory0.Find() == nil {
			_, _, _ = procFwpmFreeMemory0.Call(uintptr(unsafe.Pointer(&appBlob)))
		}
	}()

	cond := fwpmFilterCondition0{
		fieldKey:  condALEAppID,
		matchType: fwpMatchEqual,
		conditionValue: fwpConditionValue0{
			valType: fwpByteBlobType,
			value:   uintptr(unsafe.Pointer(appBlob)),
		},
	}

	name, _ := windows.UTF16PtrFromString("Permit NeoBox App")
	layers := []windows.GUID{layerALEAuthConnectV4, layerALEAuthConnectV6}
	for _, layer := range layers {
		filter := fwpmFilter0{
			displayData:         fwpmDisplayData0{name: name},
			providerKey:         &provider,
			layerKey:            layer,
			subLayerKey:         sublayer,
			weight:              fwpValue0{valType: fwpUint32, value: uintptr(weight)},
			numFilterConditions: 1,
			filterCondition:     &cond,
			action:              fwpmAction0{actionType: fwpActionPermit},
		}
		var filterID uint64
		r1, _, err := procFwpmFilterAdd0.Call(engine, uintptr(unsafe.Pointer(&filter)), 0, uintptr(unsafe.Pointer(&filterID)))
		if r1 != 0 {
			return err
		}
	}
	return nil
}

func addPermitServerIP4Filter(engine uintptr, provider, sublayer windows.GUID, weight uint8, ip4 net.IP) error {
	hostOrderIP := binary.BigEndian.Uint32(ip4)
	cond := fwpmFilterCondition0{
		fieldKey:  condRemoteAddress,
		matchType: fwpMatchEqual,
		conditionValue: fwpConditionValue0{
			valType: fwpUint32,
			value:   uintptr(hostOrderIP),
		},
	}

	name, _ := windows.UTF16PtrFromString("Permit VPN Server IPv4")
	filter := fwpmFilter0{
		displayData:         fwpmDisplayData0{name: name},
		providerKey:         &provider,
		layerKey:            layerALEAuthConnectV4,
		subLayerKey:         sublayer,
		weight:              fwpValue0{valType: fwpUint32, value: uintptr(weight)},
		numFilterConditions: 1,
		filterCondition:     &cond,
		action:              fwpmAction0{actionType: fwpActionPermit},
	}
	var filterID uint64
	r1, _, err := procFwpmFilterAdd0.Call(engine, uintptr(unsafe.Pointer(&filter)), 0, uintptr(unsafe.Pointer(&filterID)))
	if r1 != 0 {
		return err
	}
	return nil
}

func addPermitTunFilter(engine uintptr, provider, sublayer windows.GUID, weight uint8, luid uint64) error {
	cond := fwpmFilterCondition0{
		fieldKey:  condLocalInterface,
		matchType: fwpMatchEqual,
		conditionValue: fwpConditionValue0{
			valType: fwpUint64,
			value:   uintptr(unsafe.Pointer(&luid)),
		},
	}

	name, _ := windows.UTF16PtrFromString("Permit TUN Interface")
	layers := []windows.GUID{layerALEAuthConnectV4, layerALEAuthConnectV6, layerALEAuthRecvV4, layerALEAuthRecvV6}
	for _, layer := range layers {
		filter := fwpmFilter0{
			displayData:         fwpmDisplayData0{name: name},
			providerKey:         &provider,
			layerKey:            layer,
			subLayerKey:         sublayer,
			weight:              fwpValue0{valType: fwpUint32, value: uintptr(weight)},
			numFilterConditions: 1,
			filterCondition:     &cond,
			action:              fwpmAction0{actionType: fwpActionPermit},
		}
		var filterID uint64
		r1, _, err := procFwpmFilterAdd0.Call(engine, uintptr(unsafe.Pointer(&filter)), 0, uintptr(unsafe.Pointer(&filterID)))
		if r1 != 0 {
			return err
		}
	}
	return nil
}

func addBlockAllFilter(engine uintptr, provider, sublayer windows.GUID) error {
	name, _ := windows.UTF16PtrFromString("Block Non-Tunnel Outbound")
	layers := []windows.GUID{layerALEAuthConnectV4, layerALEAuthConnectV6}
	for _, layer := range layers {
		filter := fwpmFilter0{
			displayData:         fwpmDisplayData0{name: name},
			providerKey:         &provider,
			layerKey:            layer,
			subLayerKey:         sublayer,
			weight:              fwpValue0{valType: fwpUint32, value: 0}, // Lowest weight (fallback drop)
			numFilterConditions: 0,
			action:              fwpmAction0{actionType: fwpActionBlock},
		}
		var filterID uint64
		r1, _, err := procFwpmFilterAdd0.Call(engine, uintptr(unsafe.Pointer(&filter)), 0, uintptr(unsafe.Pointer(&filterID)))
		if r1 != 0 {
			return err
		}
	}
	return nil
}
