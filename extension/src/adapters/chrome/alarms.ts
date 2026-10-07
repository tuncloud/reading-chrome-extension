import type { Scheduler } from '../../application/ports'

export class ChromeScheduler implements Scheduler {
  async every(name: string, periodMinutes: number): Promise<void> {
    await chrome.alarms.create(name, { periodInMinutes: periodMinutes, delayInMinutes: periodMinutes })
  }
}

// onAlarm must be called at service-worker top level so Chrome re-attaches it on wake-up.
export function onAlarm(handler: (name: string) => void): void {
  chrome.alarms.onAlarm.addListener((alarm) => handler(alarm.name))
}
