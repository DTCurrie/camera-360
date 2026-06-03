#!/usr/bin/env python3
"""Characterize the JVCU360's two vendor Extension Units by issuing UVC GET_*
class requests to each implemented control selector. Maps the controls (length +
current value) — the basis for figuring out which control switches the display
mode.

IMPORTANT: this needs to run on Linux. macOS blocks Extension-Unit access (the
system camera driver owns the control interface -> libusb 'Access denied'). On
Linux you may also need to detach the uvcvideo kernel driver from interface 0
first (dev.detach_kernel_driver(0)).

Implemented control selectors (from the descriptor bmControls):
  unitID 2 (bmControls ff 03 00 00) -> CS 1..10
  unitID 3 (bmControls 00 0f 00)    -> CS 9..12
All on VideoControl interface number 0.
"""
import os
import usb.core
import usb.backend.libusb1

VID, PID, VC_INTF = 0x0711, 0x0360, 0
UNITS = {2: range(1, 11), 3: range(9, 13)}

# UVC request codes
SET_CUR, GET_CUR, GET_MIN, GET_MAX = 0x01, 0x81, 0x82, 0x83
GET_RES, GET_LEN, GET_INFO, GET_DEF = 0x84, 0x85, 0x86, 0x87
GET_IN = 0xA1  # dir=in, type=class, recipient=interface


def backend():
    for p in ("/opt/homebrew/lib/libusb-1.0.dylib", "/usr/local/lib/libusb-1.0.dylib"):
        if os.path.exists(p):
            return usb.backend.libusb1.get_backend(find_library=lambda x, _p=p: _p)
    return usb.backend.libusb1.get_backend()


dev = usb.core.find(idVendor=VID, idProduct=PID, backend=backend())
if dev is None:
    raise SystemExit("device not found")

# On Linux, free interface 0 from uvcvideo so we can issue class requests.
try:
    if dev.is_kernel_driver_active(VC_INTF):
        dev.detach_kernel_driver(VC_INTF)
        print(f"detached kernel driver from interface {VC_INTF}")
except Exception as e:
    print(f"(detach skipped: {e})")


def get(req, cs, unit, length):
    return dev.ctrl_transfer(GET_IN, req, cs << 8, (unit << 8) | VC_INTF, length)


def hx(b):
    return " ".join(f"{x:02x}" for x in b)


for unit, selectors in UNITS.items():
    print(f"\n=== Extension Unit {unit} (interface {VC_INTF}) ===")
    for cs in selectors:
        try:
            info = get(GET_INFO, cs, unit, 1)[0]
        except Exception as e:
            print(f"  CS {cs:2d}: GET_INFO failed -> {e}")
            continue
        caps = [n for bit, n in ((0x01, "GET"), (0x02, "SET"),
                                 (0x04, "DISABLED"), (0x08, "AUTOUPDATE")) if info & bit]
        try:
            length = int.from_bytes(get(GET_LEN, cs, unit, 2), "little")
        except Exception as e:
            print(f"  CS {cs:2d}: info={info:#04x}[{','.join(caps)}] GET_LEN failed -> {e}")
            continue
        vals = {}
        for name, req in (("cur", GET_CUR), ("min", GET_MIN), ("max", GET_MAX), ("def", GET_DEF)):
            try:
                vals[name] = hx(get(req, cs, unit, length))
            except Exception:
                vals[name] = "-"
        print(f"  CS {cs:2d}: len={length} info={info:#04x}[{','.join(caps)}]  "
              f"cur=[{vals['cur']}] min=[{vals['min']}] max=[{vals['max']}] def=[{vals['def']}]")
