export function detectBrowserInfo(navigatorLike = {}) {
  const brands = Array.isArray(navigatorLike.userAgentData?.brands)
    ? navigatorLike.userAgentData.brands.filter(isRealBrand)
    : [];
  const preferred = brands.find((entry) => !/^Chromium$/iu.test(entry.brand)) || brands[0];
  if (preferred) {
    return {
      name: normalizeBrowserName(preferred.brand),
      version: String(preferred.version || ""),
    };
  }

  const userAgent = String(navigatorLike.userAgent || "");
  for (const candidate of [
    { pattern: /Edg\/([\d.]+)/u, name: "Microsoft Edge" },
    { pattern: /OPR\/([\d.]+)/u, name: "Opera" },
    { pattern: /Vivaldi\/([\d.]+)/u, name: "Vivaldi" },
    { pattern: /Chromium\/([\d.]+)/u, name: "Chromium" },
    { pattern: /Chrome\/([\d.]+)/u, name: "Google Chrome" },
  ]) {
    const match = userAgent.match(candidate.pattern);
    if (match) return { name: candidate.name, version: match[1] };
  }
  return { name: "Chromium", version: "" };
}

function isRealBrand(entry) {
  const normalized = String(entry?.brand || "")
    .replace(/[^a-z]/giu, "")
    .toLowerCase();
  return (
    entry &&
    typeof entry.brand === "string" &&
    entry.brand.trim() !== "" &&
    normalized !== "notabrand"
  );
}

function normalizeBrowserName(brand) {
  if (/edge/iu.test(brand)) return "Microsoft Edge";
  if (/chrome/iu.test(brand)) return "Google Chrome";
  if (/chromium/iu.test(brand)) return "Chromium";
  return brand.trim();
}
