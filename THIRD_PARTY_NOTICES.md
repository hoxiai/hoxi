# Third-Party Notices

Hoxi is licensed under Apache-2.0 (see [LICENSE](LICENSE)). This file lists the
third-party components distributed with the Hoxi desktop application and their
licenses. Build-only tooling (TypeScript, Vite, Tailwind, test runners) is not
distributed and is therefore omitted.

The application bundle includes a standalone `opencode` sidecar and the
`Sparkle.framework` updater, both compiled or copied into the `.app`. Go modules
are statically linked into the Hoxi binary. Frontend runtime packages are bundled
into the web assets. Transitive npm runtime dependencies are recorded in
`web/package-lock.json`; their license files remain part of the source
distribution.

## Bundled executables and frameworks

| Component | Version | License | Copyright |
| --- | --- | --- | --- |
| [opencode](https://github.com/anomalyco/opencode) | v1.18+ (resolved at build) | MIT | Copyright (c) 2025 opencode |
| [SQLite](https://www.sqlite.org/copyright.html) Windows CLI | 3.54.1 | Public domain | SQLite authors |
| [Sparkle](https://github.com/sparkle-project/Sparkle) | 2.x (bundled framework) | MIT | Copyright (c) 2006-2013 Andy Matuschak; 2009-2013 Elgato Systems GmbH; 2011-2014 Kornel Lesiński; 2015-2017 Mayur Pawashe; 2014 C.W. Betts; 2014 Petroules Corporation; 2014 Big Nerd Ranch |

## Go modules

| Module | Version | License | Copyright |
| --- | --- | --- | --- |
| `git.sr.ht/~jackmordaunt/go-toast/v2` | `v2.0.3` | MIT | Copyright (c) 2023 Jack Mordaunt |
| `github.com/bep/debounce` | `v1.2.1` | MIT | Copyright (c) 2016 Bjørn Erik Pedersen |
| `github.com/chromedp/cdproto` | `v0.0.0-20250724212937-08a3db8b4327` | MIT | Copyright (c) 2016-2025 Kenneth Shaw |
| `github.com/chromedp/chromedp` | `v0.14.2` | MIT | Copyright (c) 2016-2025 Kenneth Shaw |
| `github.com/chromedp/sysutil` | `v1.1.0` | MIT | Copyright (c) 2016-2017 Kenneth Shaw |
| `github.com/davecgh/go-spew` | `v1.1.1` | ISC | Copyright (c) 2012-2016 Dave Collins <dave@davec.name> |
| `github.com/go-json-experiment/json` | `v0.0.0-20250725192818-e39067aee2d2` | BSD-3-Clause | Copyright (c) 2020 The Go Authors. All rights reserved. |
| `github.com/go-ole/go-ole` | `v1.3.0` | MIT | Copyright © 2013-2017 Yasuhiro Matsumoto, <mattn.jp@gmail.com> |
| `github.com/gobwas/httphead` | `v0.1.0` | MIT | Copyright (c) 2017 Sergey Kamardin |
| `github.com/gobwas/pool` | `v0.2.1` | MIT | Copyright (c) 2017-2019 Sergey Kamardin <gobwas@gmail.com> |
| `github.com/gobwas/ws` | `v1.4.0` | MIT | Copyright (c) 2017-2021 Sergey Kamardin <gobwas@gmail.com> |
| `github.com/godbus/dbus/v5` | `v5.1.0` | BSD-2-Clause | Copyright (c) 2013, Georg Reinke (<guelfey at gmail dot com>), Google |
| `github.com/google/uuid` | `v1.6.0` | BSD-3-Clause | Copyright (c) 2009,2014 Google Inc. All rights reserved. |
| `github.com/gorilla/websocket` | `v1.5.3` | BSD-2-Clause | Copyright (c) 2013 The Gorilla WebSocket Authors. All rights reserved. |
| `github.com/jchv/go-winloader` | `v0.0.0-20210711035445-715c2860da7e` | ISC | Copyright © 2021, John Chadwick <john@jchw.io> |
| `github.com/labstack/echo/v4` | `v4.13.3` | MIT | Copyright (c) 2021 LabStack |
| `github.com/labstack/gommon` | `v0.4.2` | MIT | Copyright (c) 2018 labstack |
| `github.com/leaanthony/debme` | `v1.2.1` | MIT | Copyright (c) 2021 Lea Anthony |
| `github.com/leaanthony/go-ansi-parser` | `v1.6.1` | MIT | Copyright (c) 2021-Present Lea Anthony |
| `github.com/leaanthony/gosod` | `v1.0.4` | MIT | Copyright (c) 2019 Lea Anthony |
| `github.com/leaanthony/slicer` | `v1.6.0` | MIT | Copyright (c) 2019 Lea Anthony |
| `github.com/leaanthony/u` | `v1.1.1` | MIT | Copyright (c) 2023-Present Lea Anthony |
| `github.com/ledongthuc/pdf` | `v0.0.0-20220302134840-0c2507a12d80` | BSD-3-Clause | Copyright (c) 2009 The Go Authors. All rights reserved. |
| `github.com/matryer/is` | `v1.4.1` | MIT | Copyright (c) 2017-2018 Mat Ryer |
| `github.com/mattn/go-colorable` | `v0.1.13` | MIT | Copyright (c) 2016 Yasuhiro Matsumoto |
| `github.com/mattn/go-isatty` | `v0.0.20` | MIT | Copyright (c) Yasuhiro MATSUMOTO <mattn.jp@gmail.com> |
| `github.com/orisano/pixelmatch` | `v0.0.0-20220722002657-fb0b55479cde` | MIT | Copyright (c) 2019-2021 Nao Yonashiro |
| `github.com/pkg/browser` | `v0.0.0-20240102092130-5ac0b6a4141c` | BSD-2-Clause | Copyright (c) 2014, Dave Cheney <dave@cheney.net> |
| `github.com/pkg/errors` | `v0.9.1` | BSD-2-Clause | Copyright (c) 2015, Dave Cheney <dave@cheney.net> |
| `github.com/pmezard/go-difflib` | `v1.0.0` | BSD-2-Clause | Copyright (c) 2013, Patrick Mezard |
| `github.com/rivo/uniseg` | `v0.4.7` | MIT | Copyright (c) 2019 Oliver Kuederle |
| `github.com/samber/lo` | `v1.49.1` | MIT | Copyright (c) 2022-2025 Samuel Berthe |
| `github.com/stretchr/testify` | `v1.11.1` | MIT | Copyright (c) 2012-2020 Mat Ryer, Tyler Bunnell and contributors. |
| `github.com/tkrajina/go-reflector` | `v0.5.8` | Apache-2.0 | Copyright 2016- Tomo Krajina |
| `github.com/valyala/bytebufferpool` | `v1.0.0` | MIT | Copyright (c) 2016 Aliaksandr Valialkin, VertaMedia |
| `github.com/valyala/fasttemplate` | `v1.2.2` | MIT | Copyright (c) 2015 Aliaksandr Valialkin |
| `github.com/wailsapp/go-webview2` | `v1.0.22` | MIT | Copyright (c) 2020 John Chadwick |
| `github.com/wailsapp/mimetype` | `v1.4.1` | MIT | Copyright (c) 2018-2020 Gabriel Vasile |
| `github.com/wailsapp/wails/v2` | `v2.13.0` | MIT | Copyright (c) 2018-Present Lea Anthony |
| `golang.org/x/crypto` | `v0.51.0` | BSD-3-Clause | Copyright 2009 The Go Authors. |
| `golang.org/x/net` | `v0.54.0` | BSD-3-Clause | Copyright 2009 The Go Authors. |
| `golang.org/x/sys` | `v0.44.0` | BSD-3-Clause | Copyright 2009 The Go Authors. |
| `golang.org/x/text` | `v0.37.0` | BSD-3-Clause | Copyright 2009 The Go Authors. |
| `gopkg.in/yaml.v3` | `v3.0.1` | Apache-2.0 | Copyright (c) 2006-2011 Kirill Simonov; 2011-2019 Canonical Ltd |

## Frontend runtime packages

| Package | Version | License | Copyright |
| --- | --- | --- | --- |
| `@pierre/diffs` | `1.5.2` | Apache-2.0 | - |
| `@xterm/addon-fit` | `0.10.0` | MIT | Copyright (c) 2019, The xterm.js authors |
| `@xterm/addon-webgl` | `0.18.0` | MIT | Copyright (c) 2018, The xterm.js authors |
| `@xterm/xterm` | `5.5.0` | MIT | Copyright (c) 2017-2019, The xterm.js authors |
| `lucide-solid` | `0.475.0` | ISC | Copyright (c) for portions of Lucide are held by Cole Bemis 2013-2022 as part of Feather (MIT). All other copyright (c) for Lucide are held by Lucide Contributors 2022. |
| `shiki` | `4.5.0` | MIT | Copyright (c) 2021 Pine Wu; 2023 Anthony Fu |
| `solid-js` | `1.9.17` | MIT | Copyright (c) 2016-2025 Ryan Carniato |

## License texts

### MIT License

Copyright (c) the respective authors listed above.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

### ISC License

Copyright (c) the respective authors listed above.

Permission to use, copy, modify, and/or distribute this software for any
purpose with or without fee is hereby granted, provided that the above
copyright notice and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY
SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN ACTION
OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF OR IN
CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.

### BSD 2-Clause License

Copyright (c) the respective authors listed above.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice, this
   list of conditions and the following disclaimer.

2. Redistributions in binary form must reproduce the above copyright notice,
   this list of conditions and the following disclaimer in the documentation
   and/or other materials provided with the distribution.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

### BSD 3-Clause License

Copyright (c) the respective authors listed above.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice, this
   list of conditions and the following disclaimer.

2. Redistributions in binary form must reproduce the above copyright notice,
   this list of conditions and the following disclaimer in the documentation
   and/or other materials provided with the distribution.

3. Neither the name of the copyright holder nor the names of its
   contributors may be used to endorse or promote products derived from
   this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

### Apache License 2.0

Components marked Apache-2.0 are licensed under the Apache License, Version
2.0. The full license text is included in [LICENSE](LICENSE).
