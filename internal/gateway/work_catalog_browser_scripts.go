package gateway

const catalog1688NextPageScript = `(() => {
  const collectRoots = (root, out = [], depth = 0) => {
    if (!root || depth > 5 || out.length >= 24) return out;
    out.push(root);
    let all = [];
    try { all = Array.from(root.querySelectorAll("*")); } catch {}
    for (const el of all) {
      if (el.shadowRoot) collectRoots(el.shadowRoot, out, depth + 1);
      if (el.tagName === "IFRAME") {
        try {
          if (el.contentDocument && el.contentDocument.body) {
            collectRoots(el.contentDocument, out, depth + 1);
          }
        } catch {}
      }
    }
    return out;
  };
  const roots = collectRoots(document);
  for (const r of roots) {
    const nextBtn = Array.from(
      r.querySelectorAll(
        "button.next-next:not([disabled]), .next-pagination-item.next:not([disabled]), li.ant-pagination-next:not(.ant-pagination-disabled) button, a.next:not(.disabled), button[aria-label*='下一页']:not([disabled]), a[title*='下一页']:not(.disabled)"
      )
    ).find((el) => {
      const cls = (typeof el.className === "string" ? el.className : "").toLowerCase();
      return !cls.includes("disabled") && el.getAttribute("aria-disabled") !== "true" && !el.disabled;
    });
    if (nextBtn) {
      nextBtn.click();
      return "true";
    }
  }
  return "false";
})()`

const catalog1688ExtractScript = `(() => {
  const clean = (s) => String(s || "").replace(/\s+/g, " ").trim();
  const decodeHtml = (s) =>
    clean(s)
      .replace(/&gt;/gi, " / ")
      .replace(/&lt;/gi, "<")
      .replace(/&amp;/gi, "&")
      .replace(/&quot;/gi, '"');
  const hostSuffix = [".1688", ".com"].join("");
  const cdnSuffix = [".alicdn", ".com"].join("");
  const ibankPrefix = ["img", "ibank", ""].join("/");
  const systemSubs = new Set([
    "www", "work", "myseller", "sell", "alisell", "offer", "detail",
    "s", "my", "air", "sycm", "open", "fuwu", "page", "cart", "trade",
    "login", "passport", "member", "pas", "show", "shop", "m", "amos",
    "p4p", "fxg", "re", "cbu01", "1688"
  ]);

  const href = String(location.href || "");
  if (!href.toLowerCase().includes("1688")) {
    return JSON.stringify({ hasCaptcha: false, items: [], hasNextPage: false, pageUrl: href });
  }

  const collectRoots = (root, out = [], depth = 0) => {
    if (!root || depth > 5 || out.length >= 24) return out;
    out.push(root);
    let all = [];
    try { all = Array.from(root.querySelectorAll("*")); } catch {}
    for (const el of all) {
      if (el.shadowRoot) collectRoots(el.shadowRoot, out, depth + 1);
      if (el.tagName === "IFRAME") {
        try {
          if (el.contentDocument && el.contentDocument.body) {
            collectRoots(el.contentDocument, out, depth + 1);
          }
        } catch {}
      }
    }
    return out;
  };

  const roots = collectRoots(document);

  const isVisible = (el) => {
    if (!el) return false;
    try {
      const st = window.getComputedStyle(el);
      if (st.display === "none" || st.visibility === "hidden" || st.opacity === "0") return false;
      const rect = el.getBoundingClientRect();
      return rect.width > 20 && rect.height > 20;
    } catch {
      return false;
    }
  };

  const hasCaptcha = roots.some((d) =>
    Array.from(
      d.querySelectorAll(
        ".baxia-dialog, #nc_1_wrapper, .nc-container, .sufei-dialog, [id*='baxia-punish']"
      )
    ).some(isVisible)
  );
  if (hasCaptcha) {
    return JSON.stringify({ hasCaptcha: true, items: [], hasNextPage: false, pageUrl: href });
  }

  const toIbank = (raw) => {
    let u = clean(raw);
    if (!u) return "";
    if (u.startsWith("//")) u = "https:" + u;
    const stripThumb = (p) =>
      p
        .split(/[?#]/)[0]
        .replace(/_(?:\d{2,4}x\d{2,4}[^.]*)?\.(?:jpg|jpeg|png|webp)$/i, (m) => {
          const baseExt = p.match(/\.(jpg|jpeg|png)/i);
          return baseExt ? "" : m;
        })
        .replace(/_\.webp$/i, "")
        .replace(/\.\d{2,4}x\d{2,4}(?:xz|q\d+)?\./gi, ".")
        .replace(/\.summ\./gi, ".");
    if (u.startsWith(ibankPrefix)) return stripThumb(u);
    const seg = "/" + ibankPrefix;
    const idx = u.indexOf(seg);
    if (idx >= 0 && u.includes(cdnSuffix)) return stripThumb(u.slice(idx + 1));
    return "";
  };

  const inferSellerFromUrl = (rawUrl) => {
    try {
      const h = new URL(rawUrl, location.origin).hostname.toLowerCase();
      if (!h.endsWith(hostSuffix)) return "";
      const sub = h.slice(0, -hostSuffix.length);
      if (sub && !sub.includes(".") && !systemSubs.has(sub)) return sub;
    } catch {}
    return "";
  };

  const g = window;
  const initData = g?.__INIT_DATA || g?.iDetailData || {};
  const globalData = initData?.globalData || g?.__GLOBAL_DATA || {};
  const tempModel = globalData?.tempModel || {};

  const knownCompanyNames = new Set(
    [
      tempModel?.companyName,
      globalData?.companyName,
      globalData?.sellerCompanyName,
      ...roots.flatMap((r) =>
        Array.from(
          r.querySelectorAll(
            ".shop-company-name, [class*='company-name' i], [class*='shop-name' i], [class*='supplier-name' i], [class*='shop-header' i] h1"
          )
        ).map((el) => clean(el.textContent))
      ),
    ]
      .map((s) => clean(s))
      .filter((s) => s.length >= 2)
  );

  const actionLabelRe = /^(编辑|查看|详情|复制|复制链接|下架|上架|推广|删除|置顶|改价|改库存|设置|更多|操作|管理|分享|预览|数据分析|优化|首页|供应产品|公司档案|工厂档案|联系方式|买家保障|进店逛逛|收藏店铺|立即订购|加入进货单|阿里巴巴|1688)$/;
  const pureCompanyRe = /^[\u4e00-\u9fa5A-Za-z0-9()（）·\-]{2,26}(?:有限公司|有限责任公司|股份有限公司|个体经营|工厂店|旗舰店|专营店|电线电缆厂|电子厂|塑胶厂|五金厂|制品厂|科技厂|实业)$/;

  const isInvalidProductTitle = (raw) => {
    const t = clean(raw);
    if (!t || t.length < 3) return true;
    if (actionLabelRe.test(t) || /^\d+$/.test(t)) return true;
    if (knownCompanyNames.has(t)) return true;
    if (pureCompanyRe.test(t)) return true;
    return false;
  };

  let detectedSeller =
    inferSellerFromUrl(href) ||
    clean(
      tempModel?.sellerLoginId ||
        globalData?.sellerLoginId ||
        g?.__INITIAL_STATE__?.user?.loginId ||
        g?.__INITIAL_STATE__?.userInfo?.loginId ||
        g?._global_header_config_?.loginId ||
        ""
    );

  if (!detectedSeller) {
    for (const d of roots) {
      for (const a of Array.from(d.querySelectorAll("a[href]")).slice(0, 120)) {
        const cand = inferSellerFromUrl(a.getAttribute("href") || "");
        if (cand) {
          detectedSeller = cand;
          break;
        }
      }
      if (detectedSeller) break;
    }
  }

  const inferStatus = (txt) => {
    if (/已下架|下架|已过期|expired|offline/i.test(txt)) return "expired";
    if (/待上架|审核中|草稿|仓库中|未上架|待发布|auditing|draft/i.test(txt)) return "TBD";
    return "published";
  };
` + catalog1688ExtractDetailPart + `
  // Mode B: Seller Workbench / Storefront / Offer List
  const seen = new Set();
  const items = [];
  let hasNextPage = false;

  const pushItem = (rawId, rawSubject, opts = {}) => {
    const id = clean(rawId);
    const subject = clean(rawSubject);
    if (!/^[1-9][0-9]{5,18}$/.test(id) || seen.has(id)) return;
    if (isInvalidProductTitle(subject)) return;
    seen.add(id);
    items.push({
      id,
      data: {
        productID: id,
        subject: subject.slice(0, 120),
        status: opts.status || "published",
        sourceUrl: opts.sourceUrl || undefined,
        saleInfo: {
          amountOnSale: opts.amountOnSale ?? null,
          priceRanges: Array.isArray(opts.priceRanges) ? opts.priceRanges : [],
        },
        skuInfos: opts.cargoNumber ? [{ skuId: "1", cargoNumber: opts.cargoNumber }] : [],
        image: { images: Array.isArray(opts.images) ? opts.images.slice(0, 5) : [] },
      },
    });
  };

  const walkStateObj = (node, depth = 0, visited = new Set()) => {
    if (!node || typeof node !== "object" || depth > 6 || visited.size > 1500 || items.length >= 100) return;
    if (visited.has(node)) return;
    visited.add(node);
    if (Array.isArray(node)) {
      for (const child of node) walkStateObj(child, depth + 1, visited);
      return;
    }
    const candId = String(node.offerId ?? node.productID ?? node.productId ?? node.itemId ?? "");
    const candTitle = clean(node.subject ?? node.offerTitle ?? node.title ?? node.fullName ?? "");
    if (/^[1-9][0-9]{6,18}$/.test(candId) && !isInvalidProductTitle(candTitle)) {
      const rawImg =
        node.imageUrl || node.imgUrl || node.mainImage || node.picUrl || node.summImageURI ||
        (Array.isArray(node.images) ? node.images[0] : "");
      const ibank = toIbank(typeof rawImg === "string" ? rawImg : rawImg?.url || rawImg?.fullPathImageURI || "");
      const rawPrice = Number(node.price ?? node.minPrice ?? node.tradePrice ?? node.unitPrice ?? NaN);
      const rawStock = Number(node.amountOnSale ?? node.canBookCount ?? node.quantity ?? node.stock ?? NaN);
      pushItem(candId, candTitle, {
        status: inferStatus(String(node.status || node.offerStatus || "")),
        amountOnSale: Number.isFinite(rawStock) && rawStock >= 0 ? rawStock : null,
        priceRanges: Number.isFinite(rawPrice) && rawPrice > 0 ? [{ startQuantity: 1, price: rawPrice }] : [],
        cargoNumber: clean(node.cargoNumber || node.outerId || node.skuCode || ""),
        images: ibank ? [ibank] : [],
      });
    }
    for (const k of Object.keys(node).slice(0, 60)) {
      walkStateObj(node[k], depth + 1, visited);
    }
  };

  for (const stateRoot of [g?.__INIT_DATA, g?.__INITIAL_STATE__, g?.__ICE_APP_CONTEXT__, g?.__DATA__]) {
    if (stateRoot) walkStateObj(stateRoot);
  }

  const extractOfferIdFromHref = (h) => {
    if (!h) return "";
    const m1 = h.match(/\/offer\/([1-9][0-9]{5,18})\.html/i);
    if (m1) return m1[1];
    const m2 = h.match(/[?&](?:offerId|productId|itemId|id)=([1-9][0-9]{6,18})(?:[&#]|$)/i);
    return m2 ? m2[1] : "";
  };

  const isCompanyElement = (el) => {
    let cur = el;
    for (let i = 0; i < 3 && cur; i++) {
      const cls = String(cur.className || "").toLowerCase();
      if (/company|shop-name|shop-header|supplier|seller-info|member-name/.test(cls)) return true;
      cur = cur.parentElement;
    }
    return false;
  };

  const extractFromContainer = (id, container, fallbackHref, primaryAnchor) => {
    if (!container || seen.has(id) || items.length >= 100) return;
    let subject = "";
    if (primaryAnchor && !isCompanyElement(primaryAnchor)) {
      const direct = clean(primaryAnchor.getAttribute("title") || primaryAnchor.textContent || "");
      if (!isInvalidProductTitle(direct)) subject = direct;
    }
    if (!subject) {
      const candidates = [];
      for (const el of Array.from(
        container.querySelectorAll(
          "a[href*='/offer/'], [class*='offer-title' i], [class*='subject' i], [class*='title' i], h3, h4, img[alt]"
        )
      )) {
        if (isCompanyElement(el)) continue;
        const t = clean(
          el.getAttribute?.("title") ||
            (el.tagName === "IMG" ? el.getAttribute("alt") : el.textContent) ||
            ""
        );
        if (!isInvalidProductTitle(t) && t.length <= 140) {
          candidates.push(t);
        }
      }
      candidates.sort((a, b) => b.length - a.length);
      subject = candidates[0] || "";
    }
    if (!subject) return;

    const rowText = clean(container.textContent || "");
    const priceMatches = rowText.match(/(?:¥|￥)\s*(\d+(?:\.\d{1,2})?)/g) || [];
    const numericPrices = Array.from(
      new Set(
        priceMatches
          .map((m) => Number(m.replace(/[¥￥\s]/g, "")))
          .filter((n) => Number.isFinite(n) && n > 0 && n < 1000000)
      )
    ).sort((a, b) => a - b);

    const stockMatch = rowText.match(/(?:库存|可售|在售数量|总量)[：:\s]*([0-9]{1,9})/);
    const cargoMatch = rowText.match(/(?:货号|单品货号|编码|SKU)[：:\s]*([A-Za-z0-9._-]{2,40})/i);
    const imgs = Array.from(container.querySelectorAll("img"))
      .map((img) =>
        toIbank(
          img.getAttribute("src") ||
            img.getAttribute("data-src") ||
            img.getAttribute("data-lazyload-src") ||
            ""
        )
      )
      .filter(Boolean);

    const normHref =
      fallbackHref && fallbackHref.startsWith("//")
        ? "https:" + fallbackHref
        : fallbackHref && fallbackHref.startsWith("http")
        ? fallbackHref
        : "";

    pushItem(id, subject, {
      status: inferStatus(rowText),
      sourceUrl: normHref,
      amountOnSale: stockMatch ? Number(stockMatch[1]) : null,
      priceRanges: numericPrices.map((p, idx) => ({
        startQuantity: idx === 0 ? 1 : idx * 10,
        price: p,
      })),
      cargoNumber: cargoMatch ? cargoMatch[1] : "",
      images: imgs,
    });
  };

  for (const d of roots) {
    const anchors = Array.from(d.querySelectorAll("a[href]"));
    const byOffer = new Map();
    for (const a of anchors) {
      const rawHref = a.getAttribute("href") || "";
      const id = extractOfferIdFromHref(rawHref);
      if (!id || seen.has(id)) continue;
      const aTxt = clean(a.getAttribute("title") || a.textContent || "");
      const prev = byOffer.get(id);
      if (!prev || (!isInvalidProductTitle(aTxt) && aTxt.length > prev.labelLen)) {
        byOffer.set(id, {
          anchor: a,
          href: rawHref,
          labelLen: isInvalidProductTitle(aTxt) ? 0 : aTxt.length,
        });
      }
    }

    for (const [id, info] of byOffer.entries()) {
      if (items.length >= 100) break;
      let container = info.anchor;
      let cur = info.anchor.parentElement;
      for (let step = 0; step < 6 && cur && cur !== d.body; step++) {
        let otherOffer = false;
        for (const subA of Array.from(cur.querySelectorAll("a[href]")).slice(0, 24)) {
          const otherId = extractOfferIdFromHref(subA.getAttribute("href") || "");
          if (otherId && otherId !== id) {
            otherOffer = true;
            break;
          }
        }
        if (otherOffer) break;
        const curCls = String(cur.className || "").toLowerCase();
        if (/shop-header|layout-header|page-container/.test(curCls)) break;
        container = cur;
        if (cur.tagName === "TR" || cur.tagName === "LI") break;
        cur = cur.parentElement;
      }
      extractFromContainer(id, container, info.href, info.anchor);
    }

    const rowNodes = Array.from(
      d.querySelectorAll(
        "tr, [role='row'], [class*='table-row' i], [class*='offer-item' i], [class*='product-item' i], [class*='goods-item' i], li"
      )
    );
    for (const row of rowNodes) {
      if (items.length >= 100) break;
      for (const key of Object.keys(row)) {
        if (key.startsWith("__reactProps$") || key.startsWith("__reactFiber$")) {
          try {
            const val = row[key];
            walkStateObj(val?.memoizedProps || val, 0);
          } catch {}
        }
      }
      const txt = row.textContent || "";
      const idMatch = txt.match(/(?:商品\s*ID|产品\s*ID|Offer\s*ID|ID|编号)[：:\s#]*([1-9][0-9]{7,18})/i);
      if (idMatch && !seen.has(idMatch[1])) {
        extractFromContainer(idMatch[1], row, "", null);
      }
    }

    const nextBtn = Array.from(
      d.querySelectorAll(
        "button.next-next:not([disabled]), .next-pagination-item.next:not([disabled]), li.ant-pagination-next:not(.ant-pagination-disabled) button, a.next:not(.disabled), button[aria-label*='下一页']:not([disabled]), a[title*='下一页']:not(.disabled)"
      )
    ).find((el) => {
      const cls = (typeof el.className === "string" ? el.className : "").toLowerCase();
      return !cls.includes("disabled") && el.getAttribute("aria-disabled") !== "true" && !el.disabled;
    });
    if (nextBtn && items.length >= 5) hasNextPage = true;
  }

  return JSON.stringify({
    hasCaptcha: false,
    sellerId: detectedSeller,
    items,
    hasNextPage,
    pageUrl: href,
  });
})()`
