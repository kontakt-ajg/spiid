// Material Symbols paths, Apache License 2.0.
const paths = {
  folder:'M80 800V160h320l80 80h400v560H80Zm80-80h640V320H448l-80-80H160v480Z',
  file:'M240 880V80h400l160 160v640H240Zm80-80h400V280H600V160H320v640Z',
  computer:'M80 760V200h800v560H560v40h120v80H280v-80h120v-40H80Zm80-80h640V280H160v400Z',
  download:'M440 640V80h80v560l160-160 56 56-256 256-256-256 56-56 160 160ZM160 880V720h80v80h480v-80h80v160H160Z',
  settings:'m400 880-16-128-88-48-120 48-80-136 104-80v-96L96 360l80-136 120 48 88-48 16-128' +
    'h160l16 128 88 48 120-48 80 136-104 80v96l104 80-80 136-120-48-88 48-16 128H400Z' +
    'm80-256a144 144 0 1 0 0-288 144 144 0 0 0 0 288Z',
  info:'M440 680h80V440h-80v240Zm0-320h80v-80h-80v80Zm40 520a400 400 0 1 1 0-800 400 400' +
    ' 0 0 1 0 800Zm0-80a320 320 0 1 0 0-640 320 320 0 0 0 0 640Z',
  link:'M280 640a160 160 0 0 1 0-320h160v80H280a80 80 0 0 0 0 160h160v80H280Zm240 0v-80h' +
    '160a80 80 0 0 0 0-160H520v-80h160a160 160 0 0 1 0 320H520ZM320 520v-80h320v80H32' +
    '0Z'
};

export function icon(name) {
  const ns = ['http:', '', 'www.w3.org', '2000', 'svg'].join('/');
  const svg = document.createElementNS(ns, 'svg');
  svg.setAttribute('viewBox', '0 0 960 960');
  svg.setAttribute('aria-hidden', 'true');
  const path = document.createElementNS(ns, 'path');
  path.setAttribute('d', paths[name] || paths.file);
  svg.append(path);
  return svg;
}
