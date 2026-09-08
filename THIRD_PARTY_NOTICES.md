# Third-Party Notices & Clean-Room Provenance

This document records the provenance of database format specifications, binary layouts, and constants used in `accdb`.

## Clean-Room Implementation Statement

`accdb` is an independent, clean-room implementation written in pure Go using only the Go standard library. It contains no source code copied, translated, or derived from LGPL or Apache-licensed projects. All Go code is original work distributed under the permissive [MIT License](LICENSE).

Binary format constants, page layouts, and offset structures of Microsoft Access databases were researched and cross-referenced from publicly available format specifications, public documentation, and open-source format notes:

## Specification & Provenance Reference Table

| Component | Format Specification Reference | Public Reference Source |
|---|---|---|
| **Database Header & XOR Mask** | Jet4/ACE Header Format | Microsoft MS-GCI specification & Jackcess format documentation |
| **Page Types & Header Layout** | Jet 2K/4K Slotted Page Architecture | MDB Tools documentation & Jackcess format documentation |
| **Table Definition (TDEF)** | TDEF Block Structure & Column Descriptors | Public MDB Tools documentation & Jackcess specification |
| **Slotted Row Layout** | Row Offset Array & Slot Flags (`0x1FFF`, `0x4000`, `0x8000`) | Microsoft MS-GCI specification & Jackcess format documentation |
| **Usage Maps (Inline / Carrier)** | Map Type 0x00 Bitmap Layout & StartPage pointer | Jackcess usage map documentation |
| **Index Leaf Page Layout** | Leaf Page 0x04 Header & Key-Location Entries | Public Jet format analysis & specification notes |

## Third-Party Open Source Projects Referenced for Specifications

1. **Jackcess**
   - Repository: [https://github.com/jahlborn/jackcess](https://github.com/jahlborn/jackcess)
   - License: Apache License 2.0
   - Role: Specification reference for Jet/ACE binary format constants and TDEF offset layouts.

2. **MDB Tools**
   - Repository: [https://github.com/mdbtools/mdbtools](https://github.com/mdbtools/mdbtools)
   - License: GNU Lesser General Public License (LGPL)
   - Role: Specification reference for Jet 3/4 binary documentation.

---

This software (`accdb`) is licensed under the MIT License. See [LICENSE](LICENSE) for full details.
