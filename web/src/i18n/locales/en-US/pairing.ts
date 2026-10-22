// web/src/i18n/locales/en-US/pairing.ts
export const pairing = {
  title: "Pair with the mobile app",
  tip: "Show a QR code the Hoxi app scans to reach this Mac over the local network. Both devices must be on the same Wi-Fi. The code carries this Mac's pairing token, so only display it where you trust the people around you",
  refreshAction: "Refresh",
  loading: "Generating the pairing QR",
  scanHint: "In the Hoxi app, open Link device, choose Hoxi computer, then scan",
  addressTip: "Local address: {url}. The address and pairing token are stored on this Mac, so share the QR only on a network you trust",
  noLanHint: "No local network address found. Connect this Mac to Wi-Fi or Ethernet, then refresh",
  qrAlt: "Pairing QR code",
  rotateAction: "Regenerate token",
  rotateConfirm: "Regenerate the pairing token? The current QR and any phone that paired with it lose LAN access until they scan the new code",
  rotateError: "Could not regenerate the pairing token",
  loadError: "Could not generate the pairing QR",
};
