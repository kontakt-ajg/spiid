export function humanSize(bytes) {
  if (bytes < 1000) return `${bytes} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let value = bytes / 1000, i = 0;
  while (value >= 1000 && i < units.length - 1) {
    value /= 1000;
    i++;
  }
  return `${value.toFixed(1)} ${units[i]}`;
}

export function shortDate(value) {
  const d = value instanceof Date ? value : new Date(value * 1000);
  const two = n => String(n).padStart(2, '0');
  return `${String(d.getFullYear()).padStart(4, '0')}-${two(d.getMonth()+1)}-${two(d.getDate())} ` +
    `${two(d.getHours())}:${two(d.getMinutes())}`;
}

export function selectionSummary(files, folders, bytes) {
  const parts = [];
  if (files > 0) parts.push(`${files} ${files === 1 ? 'file' : 'files'}`);
  if (folders > 0) parts.push(`${folders} ${folders === 1 ? 'folder' : 'folders'}`);
  return parts.join(', ') + (files === 0 ? '' : ` · ${humanSize(bytes)}`);
}

export function humanSpeed(speed) {
  return humanSize(Math.round(speed)) + '/s';
}

export function humanDuration(seconds) {
  seconds = Math.min(2**53, Math.max(0, Math.floor(seconds)));
  const pad = n => String(n).padStart(2, '0');
  return seconds < 3600 ? `${Math.floor(seconds/60)}:${pad(seconds%60)}`
    : `${Math.floor(seconds/3600)}:${pad(Math.floor(seconds/60)%60)}:${pad(seconds%60)}`;
}
