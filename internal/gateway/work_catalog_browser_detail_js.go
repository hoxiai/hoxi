package gateway

const catalog1688ExtractDetailPart = `
  // Mode A: Single Product Detail Page (/offer/<id>.html)
  const detailMatch = href.match(/\/offer\/([1-9][0-9]{4,18})\.html/i);
  if (detailMatch) {
    const id = detailMatch[1];
    try {
      window.scrollTo(0, Math.min(2000, (document.body?.scrollHeight || 1400) * 0.55));
    } catch {}

    const compEntries =
      initData?.data && typeof initData.data === "object"
        ? Object.values(initData.data)
        : [];

    let compTitle = "";
    let compImages = [];
    let compSkuModel = null;
    let compScalePrices = [];
    const compAttrs = [];
    const rawPackObj = {};

    for (const entry of compEntries) {
      if (!entry || typeof entry !== "object") continue;
      const compType = String(entry.componentType || "").toLowerCase();
      const d = entry.data || entry.fields || entry;
      if (!d || typeof d !== "object") continue;

      const isShopComp = /shop|header|sign|footer|nav|recommend|similar/.test(compType);
      if (!isShopComp && !compTitle) {
        const explicitOfferTitle = clean(d.offerTitle || "");
        if (explicitOfferTitle && !isInvalidProductTitle(explicitOfferTitle)) {
          compTitle = explicitOfferTitle;
        } else if (/title/.test(compType)) {
          const cand = clean(d.title || d.subject || "");
          if (cand && !isInvalidProductTitle(cand)) compTitle = cand;
        }
      }

      if (!isShopComp && Array.isArray(d.offerImgList || d.images) && compImages.length === 0) {
        compImages = (d.offerImgList || d.images)
          .map((it) =>
            toIbank(
              typeof it === "string"
                ? it
                : it?.fullPathImageURI || it?.imageURI || it?.url || it?.imgUrl || ""
            )
          )
          .filter(Boolean);
      }

      if (!compSkuModel && (d.skuInfoMap || d.skuMap || d.skuProps || d.skuModel)) {
        compSkuModel = d.skuModel || d;
      }
      if (compScalePrices.length === 0 && Array.isArray(d.skuPriceScaleRange || d.priceRanges || d.scalePrices)) {
        compScalePrices = d.skuPriceScaleRange || d.priceRanges || d.scalePrices;
      }

      const attrArr = Array.isArray(d.offerAttributes || d.attributes || d.attrList || d.crossBorderAttrs)
        ? d.offerAttributes || d.attributes || d.attrList || d.crossBorderAttrs
        : null;
      if (attrArr) {
        for (const a of attrArr) {
          const k = clean(a?.name || a?.attributeName || a?.attrName || a?.label || "").replace(/[：:]$/, "");
          const v = clean(a?.value || a?.attributeValue || a?.attrValue || a?.text || "");
          if (k && v && compAttrs.length < 60) compAttrs.push({ attributeName: k, value: v });
        }
      }

      if (/cross|pack|logistic|freight|delivery|shipping/.test(compType) || d.unitWeight || d.packageSize || d.pieceWeightScale) {
        Object.assign(rawPackObj, d);
      }
    }

    const domTitleSelectors = [
      ".module-od-title .title-text",
      "[id*='title' i] .title-text",
      ".title-text",
      "[class*='offer-title' i]",
      "[class*='TitleText']",
      ".title-first-column h1",
    ];
    let domTitle = "";
    for (const sel of domTitleSelectors) {
      for (const r of roots) {
        const el = r.querySelector(sel);
        const cand = clean(el?.textContent || "");
        if (cand && !isInvalidProductTitle(cand)) {
          domTitle = cand;
          break;
        }
      }
      if (domTitle) break;
    }

    const docTitle = clean(
      document.title
        .replace(/\s*[-|_]\s*阿里巴巴.*$/i, "")
        .replace(/\s*[-|_]\s*1688.*$/i, "")
    );

    const subjectCandidates = [
      clean(tempModel?.offerTitle || globalData?.subject || ""),
      compTitle,
      domTitle,
      docTitle,
    ];
    const subject = subjectCandidates.find((c) => !isInvalidProductTitle(c)) || docTitle;
    if (!subject) {
      return JSON.stringify({ hasCaptcha: false, sellerId: detectedSeller, items: [], hasNextPage: false, pageUrl: href });
    }

    const categoryName = clean(
      tempModel?.postCategoryName || tempModel?.categoryName || globalData?.categoryName || ""
    );

    const structuredImgs = Array.isArray(globalData?.images)
      ? globalData.images
          .map((it) => toIbank(typeof it === "string" ? it : it?.fullPathImageURI || it?.imageURI || ""))
          .filter(Boolean)
      : compImages;
    const domImgs = roots
      .flatMap((r) => Array.from(r.querySelectorAll(".detail-gallery-img img, [class*='gallery' i] img, [class*='main-img' i] img")))
      .map((img) =>
        toIbank(
          img.getAttribute("src") ||
            img.getAttribute("data-src") ||
            img.getAttribute("data-lazyload-src") ||
            ""
        )
      )
      .filter(Boolean);
    const gallery = Array.from(new Set([...structuredImgs, ...domImgs])).slice(0, 5);

    const bodyText = document.body?.innerText || "";
    const skuModel = globalData?.skuModel || initData?.skuModel || compSkuModel || {};

    const structuredRanges = Array.isArray(
      skuModel?.skuPriceScaleRange || globalData?.orderParamModel?.orderParam?.scalePrices
    )
      ? skuModel?.skuPriceScaleRange || globalData?.orderParamModel?.orderParam?.scalePrices
      : compScalePrices;
    let priceRanges = structuredRanges
      .map((r) => ({
        startQuantity: Number(r.beginAmount || r.startQuantity || r.moq || 1),
        price: Number(r.price || r.convertPrice || 0),
      }))
      .filter((r) => Number.isFinite(r.price) && r.price > 0);

    if (priceRanges.length === 0) {
      const priceMatches = bodyText.slice(0, 8000).match(/(?:¥|￥)\s*(\d+(?:\.\d{1,2})?)/g) || [];
      const prices = Array.from(
        new Set(
          priceMatches
            .map((m) => Number(m.replace(/[¥￥\s]/g, "")))
            .filter((n) => Number.isFinite(n) && n > 0 && n < 1000000)
        )
      ).sort((a, b) => b - a);
      const moqMatch = bodyText.match(/(\d+)\s*(?:件|个|双|套|只|米|条)起批/);
      const moq = moqMatch ? Number(moqMatch[1]) : 1;
      priceRanges = prices.slice(0, 3).map((p, idx) => ({
        startQuantity: idx === 0 ? moq : idx === 1 ? Math.max(10, moq * 5) : Math.max(50, moq * 20),
        price: p,
      }));
    }
    const defaultUnitPrice = priceRanges.length > 0 ? priceRanges[0].price : undefined;

    const skuProps = Array.isArray(skuModel?.skuProps) ? skuModel.skuProps : [];
    const propImageByVal = new Map();
    const propNames = [];
    for (const group of skuProps) {
      const pName = clean(group?.prop || group?.name || "");
      if (pName) propNames.push(pName);
      for (const v of Array.isArray(group?.value) ? group.value : []) {
        const vName = clean(v?.name || "");
        const vImg = toIbank(v?.imageUrl || v?.imgUrl || "");
        if (vName && vImg) propImageByVal.set(vName, vImg);
      }
    }

    const skuInfos = [];
    let totalStock = null;
    const skuInfoMap = skuModel?.skuInfoMap || skuModel?.skuMap || null;
    if (skuInfoMap && typeof skuInfoMap === "object" && Object.keys(skuInfoMap).length > 0) {
      let sumStock = 0;
      let hasStockNum = false;
      for (const [rawSpecKey, rawSku] of Object.entries(skuInfoMap)) {
        if (!rawSku || typeof rawSku !== "object") continue;
        const s = rawSku;
        const parts = String(rawSpecKey)
          .split(/&gt;|>/)
          .map((p) => decodeHtml(p))
          .filter(Boolean);
        const specLabel = parts.join(" / ") || decodeHtml(rawSpecKey);
        const skuAttrs = parts.map((val, idx) => ({
          attributeName: propNames[idx] || (idx === 0 ? "规格" : "属性" + (idx + 1)),
          attributeValue: val,
        }));
        const skuImg = parts.map((p) => propImageByVal.get(p)).find(Boolean) || undefined;
        const stockVal = Number(s.canBookCount ?? s.amountOnSale ?? s.stock ?? NaN);
        if (Number.isFinite(stockVal) && stockVal >= 0) {
          sumStock += stockVal;
          hasStockNum = true;
        }
        const rawSkuPrice = s.discountPrice ?? s.price ?? defaultUnitPrice;
        const numPrice = rawSkuPrice !== undefined && rawSkuPrice !== "" ? Number(rawSkuPrice) : NaN;
        skuInfos.push({
          skuId: String(s.skuId || s.specId || skuInfos.length + 1),
          specId: String(s.specId || ""),
          specLabel: specLabel.slice(0, 100),
          cargoNumber: clean(String(s.cargoNumber || s.skuCode || "")).slice(0, 80) || undefined,
          price: Number.isFinite(numPrice) && numPrice > 0 ? numPrice : rawSkuPrice,
          retailPrice: s.retailPrice || s.consignPrice || undefined,
          amountOnSale: Number.isFinite(stockVal) ? stockVal : undefined,
          skuImageUrl: skuImg,
          attributes: skuAttrs,
        });
        if (skuInfos.length >= 200) break;
      }
      if (hasStockNum) totalStock = sumStock;
    }

    if (skuInfos.length === 0) {
      const domSkuRows = roots.flatMap((r) =>
        Array.from(
          r.querySelectorAll(
            ".expand-view-item, .sku-item-wrapper, [class*='expand-view-item'], tr[data-sku-id]"
          )
        )
      );
      let domSumStock = 0;
      let domHasStock = false;
      for (const row of domSkuRows) {
        const label = clean(
          row.querySelector(
            ".item-label, [class*='item-label'], [class*='sku-item-name'], [class*='item-name'], td:first-child"
          )?.textContent || ""
        );
        if (!label || label.length > 80) continue;
        const rowTxt = clean(row.textContent || "");
        const pm = rowTxt.match(/(?:¥|￥)\s*(\d+(?:\.\d{1,2})?)/);
        const sm = rowTxt.match(/(\d+)\s*(?:件|个|条|双|套)?可售/);
        const priceNum = pm ? Number(pm[1]) : defaultUnitPrice;
        const stockNum = sm ? Number(sm[1]) : undefined;
        if (stockNum !== undefined) {
          domSumStock += stockNum;
          domHasStock = true;
        }
        const imgEl = row.querySelector("img");
        const skuImg = imgEl
          ? toIbank(imgEl.getAttribute("src") || imgEl.getAttribute("data-src") || "")
          : "";
        skuInfos.push({
          skuId: String(row.getAttribute("data-sku-id") || skuInfos.length + 1),
          specLabel: label,
          price: priceNum,
          amountOnSale: stockNum,
          skuImageUrl: skuImg || undefined,
          attributes: [{ attributeName: propNames[0] || "规格", attributeValue: label }],
        });
        if (skuInfos.length >= 200) break;
      }
      if (domHasStock && totalStock === null) totalStock = domSumStock;
    }

    if (skuInfos.length === 0 && skuProps.length > 0) {
      for (const group of skuProps) {
        const pName = clean(group?.prop || group?.name || "规格");
        for (const v of Array.isArray(group?.value) ? group.value : []) {
          const vName = clean(v?.name || "");
          if (!vName) continue;
          skuInfos.push({
            skuId: String(skuInfos.length + 1),
            specLabel: vName,
            price: defaultUnitPrice,
            skuImageUrl: toIbank(v?.imageUrl || "") || undefined,
            attributes: [{ attributeName: pName, attributeValue: vName }],
          });
        }
      }
    }

    if (skuInfos.length === 0) {
      roots
        .flatMap((r) =>
          Array.from(r.querySelectorAll("[class*='sku-item-name'], [class*='prop-item'] span, .obj-sku .leading"))
        )
        .map((el) => clean(el.textContent))
        .filter((s) => s.length > 0 && s.length <= 60 && !isInvalidProductTitle(s))
        .filter((s, i, arr) => arr.indexOf(s) === i)
        .slice(0, 50)
        .forEach((label, idx) => {
          skuInfos.push({
            skuId: String(idx + 1),
            specLabel: label,
            price: defaultUnitPrice,
            attributes: [{ attributeName: "规格", attributeValue: label }],
          });
        });
    }

    const attrSeen = new Set();
    const attributes = [];
    const addAttr = (rawK, rawV) => {
      const k = clean(rawK).replace(/[：:]$/, "");
      const v = clean(rawV);
      if (!k || !v || k === v || k.length > 28 || v.length > 120) return;
      if (/^(操作|数量|单价|金额|图片)$/.test(k)) return;
      const dedupKey = k + "=" + v;
      if (attrSeen.has(dedupKey) || attributes.length >= 80) return;
      attrSeen.add(dedupKey);
      attributes.push({ attributeName: k, value: v });
    };

    for (const a of compAttrs) addAttr(a.attributeName, a.value);

    for (const r of roots) {
      for (const tr of Array.from(r.querySelectorAll("table tr"))) {
        const cells = Array.from(tr.children).filter(
          (c) => c.tagName === "TH" || c.tagName === "TD"
        );
        if (cells.length >= 2 && cells.length % 2 === 0) {
          for (let j = 0; j + 1 < cells.length; j += 2) {
            addAttr(cells[j].textContent, cells[j + 1].textContent);
          }
        }
      }
      for (const item of Array.from(
        r.querySelectorAll(
          "[class*='offer-attr-item' i], [class*='attr-item' i], [class*='attribute-item' i], [class*='detail-attribute' i] li, [class*='cross-border' i] [class*='item' i]"
        )
      )) {
        const children = Array.from(item.children);
        if (children.length >= 2) {
          addAttr(children[0].textContent, children[1].textContent);
        } else {
          const parts = clean(item.textContent || "").split(/[：:]/);
          if (parts.length === 2) addAttr(parts[0], parts[1]);
        }
      }
    }

    const findAttrVal = (re) => {
      const hit = attributes.find((a) => re.test(a.attributeName));
      return hit ? hit.value : "";
    };

    const freight = globalData?.freightInfo || globalData?.logistics || rawPackObj || {};
    let weightText =
      findAttrVal(/跨境包裹重量|单品毛重|单位重量|毛重|净重|商品重量|包裹重量|weight/i) ||
      (freight?.unitWeight ? String(freight.unitWeight) + " kg" : "");
    if (!weightText) {
      const wm = bodyText.match(/(?:跨境包裹重量|单位重量|单品毛重|毛重|净重|商品重量)[：:\s]*([0-9.]+\s*(?:kg|KG|千克|g|G|克))/);
      if (wm) weightText = clean(wm[1]);
    }
    let unitWeightKg = undefined;
    if (weightText) {
      const numM = weightText.match(/([0-9]+(?:\.[0-9]+)?)\s*(kg|千克|g|克)?/i);
      if (numM) {
        const val = Number(numM[1]);
        const unit = (numM[2] || "kg").toLowerCase();
        if (Number.isFinite(val) && val > 0) {
          unitWeightKg = unit === "g" || unit === "克" ? Number((val / 1000).toFixed(4)) : val;
        }
      }
    }

    let packageSize =
      findAttrVal(/商品体积|包装尺寸|单品尺寸|包裹体积|彩盒尺寸|包装规格|体积/i) ||
      clean(freight?.packageSize || freight?.volume || "");
    if (!packageSize) {
      const dm = bodyText.match(
        /(?:商品体积|包裹体积|包装尺寸|彩盒尺寸)[：:\s]*(\d+(?:\.\d+)?\s*(?:cm|mm)?\s*[xX*×]\s*\d+(?:\.\d+)?\s*(?:cm|mm)?\s*[xX*×]\s*\d+(?:\.\d+)?\s*(?:cm|mm)?)/i
      );
      if (dm) packageSize = clean(dm[1]);
    }

    const cartonSize = findAttrVal(/外箱规格|外箱尺寸|箱规/);
    const pcsPerCarton = findAttrVal(/装箱数|装率|每箱数量|装箱规格/);
    const packingWay = findAttrVal(/包装方式|包装类型|包装$/);
    const sendGoodsAddressText =
      clean(
        freight?.sendGoodsAddressText ||
          freight?.location ||
          globalData?.deliveryAddress ||
          findAttrVal(/发货地|产地/)
      ) || "";
    const handlingTime =
      clean(freight?.deliveryLimitText || freight?.handlingTime || findAttrVal(/发货时效|预计发货/)) || "";

    const shippingInfo = {};
    if (unitWeightKg !== undefined) shippingInfo.unitWeight = unitWeightKg;
    if (weightText) shippingInfo.weightText = weightText;
    if (packageSize) shippingInfo.packageSize = packageSize;
    if (packingWay) shippingInfo.packingWay = packingWay;
    if (pcsPerCarton) shippingInfo.pcsPerCarton = pcsPerCarton;
    if (cartonSize) shippingInfo.cartonSize = cartonSize;
    if (sendGoodsAddressText) shippingInfo.sendGoodsAddressText = sendGoodsAddressText;
    if (handlingTime) shippingInfo.handlingTime = handlingTime;

    const detailEl = document.querySelector("#detail, [class*='detail-desc' i], .content-detail");
    const detailImgs = Array.from(detailEl ? detailEl.querySelectorAll("img") : [])
      .map((img) =>
        toIbank(
          img.getAttribute("data-lazyload-src") ||
            img.getAttribute("data-src") ||
            img.getAttribute("src") ||
            ""
        )
      )
      .filter((p) => p && !gallery.includes(p))
      .filter((p, i, arr) => arr.indexOf(p) === i)
      .slice(0, 20);

    const attrHtml =
      attributes.length > 0
        ? "<ul>" +
          attributes.map((a) => "<li><strong>" + a.attributeName + "：</strong>" + a.value + "</li>").join("") +
          "</ul>"
        : "";
    const detailText = clean(detailEl?.textContent || "").slice(0, 4000);
    const cdnOrigin = ["https:", "", "cbu01" + cdnSuffix].join("/");
    const detailImgTags = detailImgs
      .map((p) => '<p><img src="' + cdnOrigin + "/" + p + '" alt="" /></p>')
      .join("");
    const description =
      [attrHtml, detailText ? "<p>" + detailText + "</p>" : "", detailImgTags]
        .filter(Boolean)
        .join("\n") || ("<p>" + subject + "</p>");

    return JSON.stringify({
      hasCaptcha: false,
      sellerId: detectedSeller,
      pageUrl: href,
      items: [
        {
          id,
          data: {
            productID: id,
            subject: subject.slice(0, 120),
            status: "published",
            categoryName: categoryName || undefined,
            sourceUrl: href,
            description,
            attributes,
            shippingInfo: Object.keys(shippingInfo).length > 0 ? shippingInfo : undefined,
            saleInfo: { amountOnSale: totalStock, priceRanges },
            skuInfos,
            image: { images: gallery },
          },
        },
      ],
      hasNextPage: false,
    });
  }
`
