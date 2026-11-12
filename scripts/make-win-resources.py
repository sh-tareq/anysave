#!/usr/bin/env python3
"""Builds the Windows icon files from PNGs (called by make-app-icons.sh).

  make-win-resources.py <dir with ico-<size>.png> <out.ico> <out.syso> [<out.syso> ...]

- out.ico:  a normal .ico file (PNG images in an ICO container).
- out.syso: a COFF object holding a .rsrc section with the same icon. Go links any
  *_windows_amd64.syso next to a main package into the .exe, which is what gives the
  .exe its icon in Explorer. The icon group has resource ID 1 (the tray icon loads it).
No third-party tools: the formats are simple enough to write by hand.
"""
import struct
import sys

SIZES = [16, 24, 32, 48, 64, 128, 256]
RT_ICON, RT_GROUP_ICON, LANG_EN_US = 3, 14, 0x0409
GROUP_ID = 1


def b(n):  # ICO stores 256 as 0
    return 0 if n >= 256 else n


def make_ico(images):
    out = struct.pack("<HHH", 0, 1, len(images))
    offset = 6 + 16 * len(images)
    for size, png in images:
        out += struct.pack("<BBBBHHII", b(size), b(size), 0, 0, 1, 32, len(png), offset)
        offset += len(png)
    return out + b"".join(png for _, png in images)


def make_rsrc_section(images):
    """Returns (section bytes, offsets of the fields that hold a data address)."""
    # Resources: one RT_ICON per image (IDs 1..n) and one RT_GROUP_ICON listing them.
    group = struct.pack("<HHH", 0, 1, len(images))
    for i, (size, png) in enumerate(images, 1):
        group += struct.pack("<BBBBHHIH", b(size), b(size), 0, 0, 1, 32, len(png), i)
    types = [  # (type id, [(resource id, data)]) sorted by id, as the format requires
        (RT_ICON, [(i, png) for i, (_, png) in enumerate(images, 1)]),
        (RT_GROUP_ICON, [(GROUP_ID, group)]),
    ]
    n_res = sum(len(r) for _, r in types)

    DIR, ENTRY, DATA = 16, 8, 16
    root_size = DIR + ENTRY * len(types)
    type_dirs_size = sum(DIR + ENTRY * len(r) for _, r in types)
    lang_dirs_at = root_size + type_dirs_size
    data_entries_at = lang_dirs_at + n_res * (DIR + ENTRY)
    blobs_at = data_entries_at + n_res * DATA

    def directory(n_ids):
        return struct.pack("<IIHHHH", 0, 0, 0, 0, 0, n_ids)

    SUBDIR = 0x80000000
    root = directory(len(types))
    type_dirs = b""
    lang_dirs = b""
    data_entries = b""
    blobs = b""
    relocs = []
    type_dir_at = root_size
    k = 0  # running resource index
    for type_id, resources in types:
        root += struct.pack("<II", type_id, SUBDIR | type_dir_at)
        td = directory(len(resources))
        for res_id, data in resources:
            lang_at = lang_dirs_at + k * (DIR + ENTRY)
            entry_at = data_entries_at + k * DATA
            td += struct.pack("<II", res_id, SUBDIR | lang_at)
            lang_dirs += directory(1) + struct.pack("<II", LANG_EN_US, entry_at)
            blob_at = blobs_at + len(blobs)
            relocs.append(entry_at)  # the linker turns this section offset into an address
            data_entries += struct.pack("<IIII", blob_at, len(data), 0, 0)
            blobs += data + b"\0" * (-len(data) % 8)
            k += 1
        type_dirs += td
        type_dir_at += len(td)
    return root + type_dirs + lang_dirs + data_entries + blobs, relocs


def make_syso(images):
    section, relocs = make_rsrc_section(images)
    IMAGE_FILE_MACHINE_AMD64 = 0x8664
    IMAGE_REL_AMD64_ADDR32NB = 3
    raw_at = 20 + 40
    relocs_at = raw_at + len(section)
    symbols_at = relocs_at + 10 * len(relocs)
    header = struct.pack("<HHIIIHH", IMAGE_FILE_MACHINE_AMD64, 1, 0, symbols_at, 1, 0, 0)
    # initialized data + readable
    sect = struct.pack("<8sIIIIIIHHI", b".rsrc", 0, 0, len(section), raw_at, relocs_at, 0, len(relocs), 0, 0x40000040)
    reloc_table = b"".join(struct.pack("<IIH", at, 0, IMAGE_REL_AMD64_ADDR32NB) for at in relocs)
    symbol = struct.pack("<8sIhHBB", b".rsrc", 0, 1, 0, 3, 0)  # static symbol for section 1
    string_table = struct.pack("<I", 4)
    return header + sect + section + reloc_table + symbol + string_table


def main():
    src, ico_out, syso_outs = sys.argv[1], sys.argv[2], sys.argv[3:]
    images = []
    for size in SIZES:
        with open(f"{src}/ico-{size}.png", "rb") as f:
            images.append((size, f.read()))
    with open(ico_out, "wb") as f:
        f.write(make_ico(images))
    print("✓", ico_out)
    syso = make_syso(images)
    for path in syso_outs:
        with open(path, "wb") as f:
            f.write(syso)
        print("✓", path)


if __name__ == "__main__":
    main()
