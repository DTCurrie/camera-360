#!/usr/bin/env python3
"""Dump the UVC class-specific descriptors for the j5create JVCU360 (MCT 0711:0360).

Highlights any VideoControl Extension Unit (the vendor control unit a companion
app uses to switch modes) and lists VideoStreaming formats/frames so we can see
the real resolution groups (incl. MJPEG / H.264 / uncompressed).

Requires pyusb + libusb:  pip install pyusb  (and `brew install libusb` on macOS).
Note: reading descriptors works on macOS, but issuing Extension-Unit control
requests (see uvc_extension_probe.py) is blocked there — use Linux for that."""
import os
import sys
import usb.core
import usb.backend.libusb1
import usb.util

VID, PID = 0x0711, 0x0360


def backend():
    for p in ("/opt/homebrew/lib/libusb-1.0.dylib", "/usr/local/lib/libusb-1.0.dylib"):
        if os.path.exists(p):
            return usb.backend.libusb1.get_backend(find_library=lambda x, _p=p: _p)
    return usb.backend.libusb1.get_backend()  # default search (Linux: libusb-1.0.so)


dev = usb.core.find(idVendor=VID, idProduct=PID, backend=backend())
if dev is None:
    sys.exit("device 0711:0360 not found")

print(f"Device {VID:04x}:{PID:04x}  bcdUSB={dev.bcdUSB:04x}  "
      f"class={dev.bDeviceClass:#04x}  numconfigs={dev.bNumConfigurations}")
try:
    print(f"  Manufacturer: {usb.util.get_string(dev, dev.iManufacturer)}")
    print(f"  Product:      {usb.util.get_string(dev, dev.iProduct)}")
except Exception as e:
    print(f"  (string read skipped: {e})")

# Pull the full configuration descriptor blob via EP0 GET_DESCRIPTOR. This returns
# every class-specific descriptor inline, in order — the most complete view.
GET_DESCRIPTOR, DT_CONFIG = 0x06, 0x02
hdr = dev.ctrl_transfer(0x80, GET_DESCRIPTOR, DT_CONFIG << 8, 0, 9)
wTotalLength = hdr[2] | (hdr[3] << 8)
blob = bytes(dev.ctrl_transfer(0x80, GET_DESCRIPTOR, DT_CONFIG << 8, 0, wTotalLength))
print(f"\nConfiguration descriptor: {wTotalLength} bytes\n")

CS_INTERFACE = 0x24
VC, VS = 0x01, 0x02
vc_sub = {1: "HEADER", 2: "INPUT_TERMINAL", 3: "OUTPUT_TERMINAL",
          4: "SELECTOR_UNIT", 5: "PROCESSING_UNIT", 6: "EXTENSION_UNIT",
          7: "ENCODING_UNIT"}
vs_sub = {1: "INPUT_HEADER", 2: "OUTPUT_HEADER", 3: "STILL_FRAME",
          4: "FORMAT_UNCOMPRESSED", 5: "FRAME_UNCOMPRESSED",
          6: "FORMAT_MJPEG", 7: "FRAME_MJPEG", 0x0d: "COLORFORMAT",
          0x10: "FORMAT_FRAME_BASED", 0x11: "FRAME_FRAME_BASED"}


def guid(b):
    d1 = int.from_bytes(b[0:4], "little")
    d2 = int.from_bytes(b[4:6], "little")
    d3 = int.from_bytes(b[6:8], "little")
    rest = b[8:16]
    return f"{{{d1:08x}-{d2:04x}-{d3:04x}-{rest[0]:02x}{rest[1]:02x}-" + \
        "".join(f"{x:02x}" for x in rest[2:8]) + "}"


i = 0
cur_class = None
cur_subclass = None
while i < len(blob):
    blen = blob[i]
    if blen == 0:
        break
    dtype = blob[i + 1]
    d = blob[i:i + blen]
    if dtype == 0x04:  # INTERFACE
        cur_class = d[5]
        cur_subclass = d[6]
        print(f"INTERFACE  num={d[2]} alt={d[3]} class={d[5]:#04x} "
              f"subclass={d[6]:#04x} eps={d[4]}")
    elif dtype == 0x05:  # ENDPOINT
        print(f"  ENDPOINT  addr={d[2]:#04x} attr={d[3]:#04x} "
              f"maxpkt={d[4] | (d[5] << 8)}")
    elif dtype == CS_INTERFACE:
        sub = d[2]
        if cur_class == 0x0e and cur_subclass == VC:
            name = vc_sub.get(sub, f"sub{sub:#04x}")
            if sub == 6:  # EXTENSION_UNIT — the interesting one
                unit_id = d[3]
                g = guid(d[4:20])
                num_ctrl = d[20]
                nr_in = d[21]
                src = list(d[22:22 + nr_in])
                cs_off = 22 + nr_in
                ctrl_size = d[cs_off]
                bm = d[cs_off + 1:cs_off + 1 + ctrl_size]
                print(f"  CS_VC EXTENSION_UNIT  unitID={unit_id} "
                      f"numControls={num_ctrl} nrInPins={nr_in} src={src}")
                print(f"      GUID={g}")
                print(f"      bControlSize={ctrl_size} "
                      f"bmControls={' '.join(f'{x:02x}' for x in bm)}")
            else:
                print(f"  CS_VC {name}")
        elif cur_class == 0x0e and cur_subclass == VS:
            name = vs_sub.get(sub, f"sub{sub:#04x}")
            if sub in (5, 7, 0x11):  # FRAME descriptors carry w/h
                w = d[5] | (d[6] << 8)
                h = d[7] | (d[8] << 8)
                print(f"  CS_VS {name}  idx={d[3]} {w}x{h}")
            elif sub in (4, 6, 0x10):  # FORMAT descriptors
                gtxt = ""
                if sub in (4, 0x10) and blen >= 21:
                    gtxt = "  GUID=" + guid(d[5:21])
                print(f"  CS_VS {name}  fmtIdx={d[3]} numFrames={d[4]}{gtxt}")
            else:
                print(f"  CS_VS {name}")
        else:
            print(f"  CS_INTERFACE class={cur_class} subclass={cur_subclass} sub={sub:#04x}")
    i += blen
