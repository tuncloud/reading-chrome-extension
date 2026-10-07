// deviceName labels this browser in the user's device list (spec §2.3 device_name).
export function deviceName(nav: Navigator): string {
  const hinted = (nav as Navigator & { userAgentData?: { platform?: string } }).userAgentData?.platform
  if (hinted) return `Chrome on ${hinted}`
  const ua = nav.userAgent ?? ''
  const platform = /CrOS/.test(ua) ? 'ChromeOS'
    : /Windows/.test(ua) ? 'Windows'
      : /Mac OS X|Macintosh/.test(ua) ? 'macOS'
        : /Android/.test(ua) ? 'Android'
          : /Linux/.test(ua) ? 'Linux'
            : ''
  return platform ? `Chrome on ${platform}` : 'Chrome'
}
