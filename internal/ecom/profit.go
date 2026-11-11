package ecom

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// NormalizePlatform maps common e-commerce platform aliases to canonical IDs.
func NormalizePlatform(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "amazon", "amz", "amazon-seller", "fba":
		return "amazon"
	case "tiktok-shop", "tiktok", "tts", "tiktok_shop", "tk":
		return "tiktok-shop"
	case "shopify", "dtc", "independent", "standalone", "woocommerce":
		return "shopify"
	case "1688", "alibaba", "sourcing":
		return "1688"
	default:
		return s
	}
}

// CalculateProfit performs deterministic unit-economics, landed-cost, FBA tier,
// ROAS sensitivity, and target-margin pricing calculations.
func CalculateProfit(req ProfitRequest, defaults *StoreDefaults) (ProfitBreakdown, error) {
	applyProfitDefaults(&req, defaults)
	platform := NormalizePlatform(req.Platform)
	switch platform {
	case "amazon", "tiktok-shop", "shopify", "1688":
	default:
		return ProfitBreakdown{}, fmt.Errorf("unsupported platform %q (expected amazon, tiktok-shop, shopify, or 1688)", req.Platform)
	}
	if platform != "1688" && req.Price <= 0 && req.TargetNetMarginPct <= 0 {
		return ProfitBreakdown{}, errors.New("price must be greater than 0")
	}
	if req.Price < 0 {
		return ProfitBreakdown{}, errors.New("price cannot be negative")
	}

	currency := strings.ToUpper(strings.TrimSpace(req.Currency))
	if currency == "" {
		if platform == "1688" && req.FXRate <= 0 {
			currency = "CNY"
		} else {
			currency = "USD"
		}
	}

	fx := req.FXRate
	if fx <= 0 {
		if currency == "CNY" || currency == "RMB" {
			fx = 1.0
		} else {
			fx = 7.20
		}
	}

	divisor := req.VolumetricDivisor
	if divisor <= 0 {
		divisor = 6000
	}
	var volKg float64
	if req.LengthCm > 0 && req.WidthCm > 0 && req.HeightCm > 0 {
		volKg = (req.LengthCm * req.WidthCm * req.HeightCm) / divisor
	}
	chargeableKg := math.Max(math.Max(0, req.ActualWeightKg), volKg)

	qty := req.OrderQty
	if qty <= 0 {
		qty = 100
	}

	// 1. Sourcing & First-Leg Landed Cost.
	var sourcingUnit float64
	selectedBracketCNY := selectBracketUnitCost(req.PriceBrackets, qty, req.ProductCostCNY)
	if req.ProductCost > 0 {
		sourcingUnit = req.ProductCost
	} else if selectedBracketCNY > 0 {
		unitCNY := selectedBracketCNY + req.DomesticShipCNY
		sourcingUnit = unitCNY / fx
	}

	var firstLegShip float64
	if req.ShippingCost > 0 {
		firstLegShip = req.ShippingCost
	} else if req.ShippingRatePerKg > 0 && chargeableKg > 0 {
		shipCur := ""
		if defaults != nil {
			shipCur = strings.ToUpper(strings.TrimSpace(defaults.ShippingCurrency))
		}
		if (shipCur == "" && fx > 1 && currency != "CNY") || shipCur == "CNY" || shipCur == "RMB" {
			firstLegShip = (chargeableKg * req.ShippingRatePerKg) / fx
		} else {
			firstLegShip = chargeableKg * req.ShippingRatePerKg
		}
	}

	tariffAndPrep := sourcingUnit*(math.Max(0, req.TariffPct)/100.0) + math.Max(0, req.PrepFee)
	landedCost := sourcingUnit + firstLegShip + tariffAndPrep

	out := ProfitBreakdown{
		Platform:           platform,
		Currency:           currency,
		Price:              round2(req.Price),
		VolumetricWeightKg: round3(volKg),
		ChargeableWeightKg: round3(chargeableKg),
		SourcingUnitCost:   round2(sourcingUnit),
		FirstLegShipping:   round2(firstLegShip),
		TariffAndPrep:      round2(tariffAndPrep),
		LandedUnitCost:     round2(landedCost),
		TargetNetMarginPct: round2(req.TargetNetMarginPct),
	}

	if platform == "1688" {
		out.TierQuotes = build1688TierQuotes(req, chargeableKg, fx)
		if req.Price <= 0 {
			target := req.TargetNetMarginPct
			if target <= 0 {
				target = 20
				out.TargetNetMarginPct = 20
			}
			// Typical cross-border retail benchmark: 15% platform + 15% ad budget + target net margin.
			denom := 1.0 - 0.30 - (target / 100.0)
			if denom > 0.05 && landedCost > 0 {
				out.RecommendedPrice = round2(landedCost / denom)
			}
			return out, nil
		}
	}

	// 2. Platform-specific fees & fulfillment.
	refPct := req.ReferralFeePct
	var minRef, platformFee, affiliateFee, gatewayFee, fulfillmentCost, sampleAmort, returnLoss float64

	switch platform {
	case "amazon":
		if refPct <= 0 {
			refPct = 15.0
		}
		minRef = req.MinReferralFee
		if minRef <= 0 && currency == "USD" {
			minRef = 0.30
		}
		platformFee = math.Max(req.Price*(refPct/100.0), minRef)
		fba := req.FBAFee
		if fba > 0 {
			out.FBASizeTier = "Custom"
		} else if req.ActualWeightKg > 0 || volKg > 0 {
			fba, out.FBASizeTier = estimateUSFBAFee(req.LengthCm, req.WidthCm, req.HeightCm, req.ActualWeightKg)
		}
		fulfillmentCost = fba + math.Max(0, req.FulfillmentFee) + math.Max(0, req.StorageFee)
		retRate := math.Max(0, req.ReturnRatePct) / 100.0
		adminLoss := math.Min(5.0, 0.20*platformFee)
		returnLoss = retRate * (fulfillmentCost + adminLoss)

	case "tiktok-shop":
		if refPct <= 0 {
			refPct = 6.0
		}
		platformFee = req.Price * (refPct / 100.0)
		affiliateFee = req.Price * (math.Max(0, req.AffiliatePct) / 100.0)
		fulfillmentCost = math.Max(0, req.FulfillmentFee) + math.Max(0, req.ShippingSubsidy) + math.Max(0, req.StorageFee)
		if req.SampleCount > 0 && req.SampleUnitCost > 0 {
			denom := req.ExpectedUnitsSold
			if denom <= 0 {
				denom = 100
			}
			sampleAmort = (req.SampleUnitCost * float64(req.SampleCount)) / float64(denom)
		}
		retRate := math.Max(0, req.ReturnRatePct) / 100.0
		returnLoss = retRate * (fulfillmentCost + 0.20*platformFee)

	case "shopify":
		gwPct := req.GatewayFeePct
		gwFixed := req.GatewayFixedFee
		if gwPct <= 0 && gwFixed <= 0 {
			gwPct = 2.9
			gwFixed = 0.30
		}
		gatewayFee = req.Price*(gwPct/100.0) + gwFixed
		platformFee = req.Price * (math.Max(0, req.PlatformTxFeePct+refPct) / 100.0)
		fulfillmentCost = math.Max(0, req.FulfillmentFee) + math.Max(0, req.ShippingSubsidy) + math.Max(0, req.StorageFee)
		retRate := math.Max(0, req.ReturnRatePct) / 100.0
		returnLoss = retRate * (fulfillmentCost + gatewayFixedPortion(gwFixed))

	case "1688":
		if refPct > 0 {
			platformFee = req.Price * (refPct / 100.0)
		}
		fulfillmentCost = math.Max(0, req.FulfillmentFee)
	}

	profitBeforeAds := req.Price - landedCost - platformFee - affiliateFee - gatewayFee - fulfillmentCost - sampleAmort - returnLoss

	var adCost, adRatio float64
	if req.AdSpendPerUnit > 0 {
		adCost = req.AdSpendPerUnit
	} else if req.ACOSPct > 0 {
		adRatio = req.ACOSPct / 100.0
		adCost = req.Price * adRatio
	} else if req.ROAS > 0 {
		adRatio = 1.0 / req.ROAS
		adCost = req.Price * adRatio
	}

	netProfit := profitBeforeAds - adCost
	out.PlatformFee = round2(platformFee)
	out.AffiliateFee = round2(affiliateFee)
	out.PaymentGatewayFee = round2(gatewayFee)
	out.FulfillmentCost = round2(fulfillmentCost)
	out.SampleAmortization = round2(sampleAmort)
	out.ReturnLossBuffer = round2(returnLoss)
	out.ProfitBeforeAds = round2(profitBeforeAds)
	out.AdCostPerUnit = round2(adCost)
	out.NetProfit = round2(netProfit)

	if req.Price > 0 {
		out.GrossMarginPct = round2((profitBeforeAds / req.Price) * 100.0)
		out.NetMarginPct = round2((netProfit / req.Price) * 100.0)
		if profitBeforeAds > 0 {
			out.BreakEvenACOSPct = round2((profitBeforeAds / req.Price) * 100.0)
			out.BreakEvenROAS = round2(req.Price / profitBeforeAds)
		}
	}
	if landedCost > 0 {
		out.UnitROIPct = round2((netProfit / landedCost) * 100.0)
	}

	targetPct := req.TargetNetMarginPct
	if targetPct == 0 {
		targetPct = 15.0
		out.TargetNetMarginPct = 15.0
	}
	if req.Price > 0 {
		out.MaxCPAForTarget = round2(profitBeforeAds - req.Price*(targetPct/100.0))
	}

	// Reverse price solver for TargetNetMarginPct.
	fixedCosts := landedCost + fulfillmentCost + sampleAmort + returnLoss
	if req.AdSpendPerUnit > 0 {
		fixedCosts += req.AdSpendPerUnit
	}
	varPct := (refPct + math.Max(0, req.AffiliatePct) + math.Max(0, req.PlatformTxFeePct)) / 100.0
	if platform == "shopify" {
		gwPct := req.GatewayFeePct
		gwFixed := req.GatewayFixedFee
		if gwPct <= 0 && gwFixed <= 0 {
			gwPct = 2.9
			gwFixed = 0.30
		}
		varPct = (math.Max(0, req.PlatformTxFeePct+refPct) + gwPct) / 100.0
		fixedCosts += gwFixed
	}
	denom := 1.0 - varPct - adRatio - (targetPct / 100.0)
	if denom > 0.02 && fixedCosts > 0 {
		out.RecommendedPrice = round2(fixedCosts / denom)
	}

	if (platform == "shopify" || platform == "tiktok-shop") && req.Price > 0 {
		for _, roas := range []float64{1.5, 2.0, 2.5, 3.0, 4.0} {
			cpa := req.Price / roas
			np := profitBeforeAds - cpa
			out.ROASMatrix = append(out.ROASMatrix, ROASSimulationRow{
				ROAS:         roas,
				AdCPA:        round2(cpa),
				NetProfit:    round2(np),
				NetMarginPct: round2((np / req.Price) * 100.0),
			})
		}
	}

	if req.Price > 0 && netProfit < 0 {
		out.Warnings = append(out.Warnings, "Net profit is negative at current price and ad spend; raise price, cut landed/fulfillment cost, or lower CPA/ACOS.")
	} else if req.Price > 0 && out.NetMarginPct < targetPct {
		out.Warnings = append(out.Warnings, fmt.Sprintf("Net margin %.2f%% is below target %.2f%% (recommended price: %.2f %s).", out.NetMarginPct, targetPct, out.RecommendedPrice, currency))
	}
	return out, nil
}

func applyProfitDefaults(req *ProfitRequest, d *StoreDefaults) {
	if d == nil {
		return
	}
	if strings.TrimSpace(req.Platform) == "" {
		req.Platform = d.Platform
	}
	if strings.TrimSpace(req.Marketplace) == "" {
		req.Marketplace = d.Marketplace
	}
	if strings.TrimSpace(req.Currency) == "" {
		req.Currency = d.Currency
	}
	if req.ReferralFeePct <= 0 && d.ReferralFeePct > 0 {
		req.ReferralFeePct = d.ReferralFeePct
	}
	if req.TargetNetMarginPct == 0 && d.TargetNetMarginPct != 0 {
		req.TargetNetMarginPct = d.TargetNetMarginPct
	}
	if req.ShippingRatePerKg <= 0 && d.ShippingRatePerKg > 0 {
		req.ShippingRatePerKg = d.ShippingRatePerKg
	}
	if req.FXRate <= 0 && d.FXRate > 0 {
		req.FXRate = d.FXRate
	}
	if req.ReturnRatePct <= 0 && d.ReturnRatePct > 0 {
		req.ReturnRatePct = d.ReturnRatePct
	}
	if req.ACOSPct <= 0 && req.ROAS <= 0 && req.AdSpendPerUnit <= 0 {
		if d.TargetACOSPct > 0 {
			req.ACOSPct = d.TargetACOSPct
		} else if d.TargetROAS > 0 {
			req.ROAS = d.TargetROAS
		}
	}
}

func selectBracketUnitCost(brackets []WholesaleBracket, qty int, fallbackCNY float64) float64 {
	if len(brackets) == 0 {
		return fallbackCNY
	}
	sorted := make([]WholesaleBracket, len(brackets))
	copy(sorted, brackets)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].MinQty < sorted[j].MinQty })
	chosen := sorted[0].UnitCostCNY
	for _, b := range sorted {
		if qty >= b.MinQty && b.UnitCostCNY > 0 {
			chosen = b.UnitCostCNY
		}
	}
	return chosen
}

func build1688TierQuotes(req ProfitRequest, chargeableKg, fx float64) []TierQuoteRow {
	var tiers []WholesaleBracket
	if len(req.PriceBrackets) > 0 {
		tiers = append(tiers, req.PriceBrackets...)
		sort.Slice(tiers, func(i, j int) bool { return tiers[i].MinQty < tiers[j].MinQty })
	} else if req.ProductCostCNY > 0 {
		tiers = []WholesaleBracket{
			{MinQty: 100, UnitCostCNY: req.ProductCostCNY},
			{MinQty: 500, UnitCostCNY: round2(req.ProductCostCNY * 0.95)},
			{MinQty: 1000, UnitCostCNY: round2(req.ProductCostCNY * 0.90)},
		}
	}
	shipUnitCNY := chargeableKg * math.Max(0, req.ShippingRatePerKg)
	var rows []TierQuoteRow
	for _, t := range tiers {
		if t.MinQty <= 0 || t.UnitCostCNY <= 0 {
			continue
		}
		exwPlusDom := t.UnitCostCNY + math.Max(0, req.DomesticShipCNY)
		tariffCNY := exwPlusDom * (math.Max(0, req.TariffPct) / 100.0)
		landedCNY := exwPlusDom + shipUnitCNY + tariffCNY + math.Max(0, req.PrepFee)*fx
		rows = append(rows, TierQuoteRow{
			Qty:                t.MinQty,
			UnitCostCNY:        round2(t.UnitCostCNY),
			FirstLegShipCNY:    round2(shipUnitCNY),
			LandedCostCNY:      round2(landedCNY),
			LandedCostCurrency: round2(landedCNY / fx),
			TotalBatchCNY:      round2(landedCNY * float64(t.MinQty)),
		})
	}
	return rows
}

func estimateUSFBAFee(lCm, wCm, hCm, actualKg float64) (float64, string) {
	sidesIn := []float64{lCm / 2.54, wCm / 2.54, hCm / 2.54}
	sort.Float64s(sidesIn)
	shortIn, medIn, longIn := sidesIn[0], sidesIn[1], sidesIn[2]
	actualLb := actualKg * 2.20462
	var dimLb float64
	if longIn > 0 && medIn > 0 && shortIn > 0 {
		dimLb = (longIn * medIn * shortIn) / 139.0
	}
	hasDims := longIn > 0 && medIn > 0 && shortIn > 0
	if (!hasDims || (longIn <= 15 && medIn <= 12 && shortIn <= 0.75)) && actualLb > 0 && actualLb <= 1.0 {
		oz := actualLb * 16.0
		switch {
		case oz <= 4:
			return 3.06, "Small Standard"
		case oz <= 8:
			return 3.25, "Small Standard"
		case oz <= 12:
			return 3.44, "Small Standard"
		default:
			return 3.65, "Small Standard"
		}
	}
	billLb := math.Max(actualLb, dimLb)
	if (!hasDims || (longIn <= 18 && medIn <= 14 && shortIn <= 8)) && billLb <= 20 {
		switch {
		case billLb <= 0.5:
			return 3.86, "Large Standard"
		case billLb <= 1.0:
			return 4.30, "Large Standard"
		case billLb <= 2.0:
			return 5.14, "Large Standard"
		case billLb <= 3.0:
			return 5.95, "Large Standard"
		default:
			extraHalfLb := math.Ceil((billLb - 3.0) * 2.0)
			return round2(5.95 + 0.16*extraHalfLb), "Large Standard"
		}
	}
	extraLb := math.Max(0, math.Ceil(billLb-1.0))
	return round2(9.73 + 0.42*extraLb), "Large Bulky"
}

func gatewayFixedPortion(gwFixed float64) float64 {
	if gwFixed < 0 {
		return 0
	}
	return gwFixed
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}
