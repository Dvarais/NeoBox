export let pingData = {};
export let currentSortMode = 'default';

// ── Country flag detection ────────────────────────────────────────────────────
// Maps common TLDs / hostname keywords to flag emojis.
// Falls back to 🌐 for anything unrecognised.
const COUNTRY_PATTERNS = [
  [/\.ru$|russia|moscow/i, '🇷🇺'],
  [/\.us$|usa|united.?states|new.?york|los.?angeles|chicago|dallas|seattle/i, '🇺🇸'],
  [/\.de$|germany|berlin|frankfurt/i, '🇩🇪'],
  [/\.nl$|nether|amsterdam/i, '🇳🇱'],
  [/\.fr$|france|paris/i, '🇫🇷'],
  [/\.gb$|\.uk$|uk|britain|london/i, '🇬🇧'],
  [/\.jp$|japan|tokyo|osaka/i, '🇯🇵'],
  [/\.sg$|singapore/i, '🇸🇬'],
  [/\.hk$|hongkong|hong.?kong/i, '🇭🇰'],
  [/\.kr$|korea|seoul/i, '🇰🇷'],
  [/\.tw$|taiwan|taipei/i, '🇹🇼'],
  [/\.tr$|turkey|istanbul/i, '🇹🇷'],
  [/\.ua$|ukraine|kyiv/i, '🇺🇦'],
  [/\.fi$|finland/i, '🇫🇮'],
  [/\.se$|sweden|stockholm/i, '🇸🇪'],
  [/\.no$|norway|oslo/i, '🇳🇴'],
  [/\.ch$|swiss|zurich/i, '🇨🇭'],
  [/\.at$|austria|vienna/i, '🇦🇹'],
  [/\.pl$|poland|warsaw/i, '🇵🇱'],
  [/\.cz$|czech|prague/i, '🇨🇿'],
  [/\.ca$|canada|toronto|vancouver/i, '🇨🇦'],
  [/\.au$|australia|sydney|melbourne/i, '🇦🇺'],
  [/\.br$|brazil|sao.?paulo/i, '🇧🇷'],
  [/\.in$|india|mumbai|bangalore/i, '🇮🇳'],
  [/\.id$|indonesia|jakarta/i, '🇮🇩'],
  [/\.vn$|vietnam|hanoi/i, '🇻🇳'],
  [/\.th$|thailand|bangkok/i, '🇹🇭'],
  [/\.my$|malaysia|kuala/i, '🇲🇾'],
  [/\.cn$|china|beijing|shanghai/i, '🇨🇳'],
  [/\.az$|azerbai/i, '🇦🇿'],
  [/\.kz$|kazakh/i, '🇰🇿'],
  [/\.lt$|lithua/i, '🇱🇹'],
  [/\.lv$|latvia/i, '🇱🇻'],
  [/\.ee$|estonia/i, '🇪🇪'],
  [/\.ro$|romania|bucharest/i, '🇷🇴'],
  [/\.bg$|bulgar|sofia/i, '🇧🇬'],
  [/\.rs$|serbia|belgrade/i, '🇷🇸'],
  [/\.es$|spain|madrid|barcelona/i, '🇪🇸'],
  [/\.pt$|portug|lisbon/i, '🇵🇹'],
  [/\.it$|italy|milan|rome/i, '🇮🇹'],
  [/\.mx$|mexico/i, '🇲🇽'],
  [/\.za$|south.?afri/i, '🇿🇦'],
  [/\.ae$|dubai|emirates/i, '🇦🇪'],
  [/\.il$|israel|tel.?aviv/i, '🇮🇱'],
  [/\.ar$|argenti/i, '🇦🇷'],
];

export function getCountryFlag(name, address) {
  const haystack = `${name} ${address}`.toLowerCase();
  for (const [pattern, flag] of COUNTRY_PATTERNS) {
    if (pattern.test(haystack)) return flag;
  }
  return '🌐';
}

export function parseBasicInfo(link) {
  try {
    let protocol = '';
    let rest = '';
    if (link.includes('://')) {
      const parts = link.split('://');
      protocol = parts[0].toLowerCase();
      rest = parts[1];
    }

    let name = '';
    let address = '';

    // Handle hash/fragment for name
    if (rest.includes('#')) {
      const hashParts = rest.split('#');
      rest = hashParts[0];
      try {
        name = decodeURIComponent(hashParts[1]) || '';
      } catch (e) {
        name = hashParts[1] || '';
      }
    }

    if (protocol === 'vmess') {
      try {
        const decoded = window.api.decodeBase64(rest);
        const vmessData = JSON.parse(decoded);
        name = name || vmessData.ps || '';
        address = vmessData.add || '';
      } catch (e) {
        address = 'vmess-config';
      }
    } else if (protocol === 'ss') {
      try {
        const url = new URL(link);
        name = name || decodeURIComponent(url.hash.replace('#', '')) || '';
        address = url.hostname || '';
        if (!url.username && url.hostname && !url.port) {
          const decoded = window.api.decodeBase64(url.hostname);
          if (decoded.includes('@')) {
            address = decoded.split('@')[1].split(':')[0];
          }
        }
      } catch (e) {
        if (rest.includes('@')) {
          address = rest.split('@')[1].split(':')[0];
        } else {
          try {
            const decoded = window.api.decodeBase64(rest);
            if (decoded.includes('@')) {
              address = decoded.split('@')[1].split(':')[0];
            }
          } catch (e2) {}
        }
      }
    } else {
      try {
        const url = new URL(link);
        name = name || decodeURIComponent(url.hash.replace('#', '')) || '';
        address = url.hostname || '';
      } catch (e) {
        let hostPort = rest;
        if (hostPort.includes('@')) hostPort = hostPort.split('@')[1];
        if (hostPort.includes('?')) hostPort = hostPort.split('?')[0];
        if (hostPort.includes('/')) hostPort = hostPort.split('/')[0];
        address = hostPort.includes(':') ? hostPort.split(':')[0] : hostPort;
      }
    }

    return {
      type: protocol === 'ss' ? 'ShadowSocks' : protocol,
      name: name,
      address: address
    };
  } catch (e) {
    // Left blank rather than filled with a placeholder: this module has no
    // language table, so the caller substitutes a localised label if it needs one.
    return { type: '', name: '', address: '' };
  }
}

export function sortServers(servers, sortMode, pingData) {
  const sorted = [...servers];
  if (sortMode === 'ping') {
    sorted.sort((a, b) => {
      const valA = pingData[a] === -1 ? 10000 : (pingData[a] || 9999);
      const valB = pingData[b] === -1 ? 10000 : (pingData[b] || 9999);
      return valA - valB;
    });
  } else if (sortMode === 'name') {
    sorted.sort((a, b) => {
      const infoA = parseBasicInfo(a);
      const infoB = parseBasicInfo(b);
      return (infoA.name || infoA.address).localeCompare(infoB.name || infoB.address);
    });
  } else if (sortMode === 'protocol') {
    sorted.sort((a, b) => {
      return parseBasicInfo(a).type.localeCompare(parseBasicInfo(b).type);
    });
  }
  return sorted;
}

export function renderCards(container, servers, activeServerLink, pingData, sortMode, onServerSelect, searchQuery, favoriteLinks, onToggleFavorite, t = {}) {
  if (!container) return;
  container.innerHTML = '';

  const uniqueServers = Array.from(new Set(servers));
  let displayServers = sortServers(uniqueServers, sortMode, pingData);

  // Apply search filter
  const q = (searchQuery || '').trim().toLowerCase();
  if (q) {
    displayServers = displayServers.filter(link => {
      const info = parseBasicInfo(link);
      return (
        (info.name || '').toLowerCase().includes(q) ||
        (info.address || '').toLowerCase().includes(q) ||
        (info.type || '').toLowerCase().includes(q)
      );
    });
  }

  displayServers.forEach(link => {
    const info = parseBasicInfo(link);
    const card = document.createElement('div');
    card.className = `server-card ${activeServerLink === link ? 'selected' : ''}`;

    let latency = pingData[link] === 'pinging' ? '...'
                : pingData[link] === -1         ? 'Err'
                : pingData[link]                ? `${pingData[link]}ms`
                : '—';

    // Colour-code latency
    let pingColor = 'var(--text-dim)';
    if (typeof pingData[link] === 'number' && pingData[link] !== -1) {
      pingColor = pingData[link] < 150 ? 'var(--success)' : pingData[link] < 400 ? '#f59e0b' : 'var(--danger)';
    }

    const displayName = info.name || info.address || t.proxyFallbackName || 'Proxy';
    const displayType = info.type ? info.type.toUpperCase() : 'VPN';
    const flag = getCountryFlag(info.name, info.address);

    const detailsDiv = document.createElement('div');
    detailsDiv.className = 'details';

    const iconDiv = document.createElement('div');
    iconDiv.className = 'server-icon';
    iconDiv.textContent = flag;

    const infoDiv = document.createElement('div');
    const titleH4 = document.createElement('h4');
    titleH4.style.cssText = 'font-size:14px; display:flex; align-items:center; gap:8px; margin:0;';

    const protoTag = document.createElement('span');
    protoTag.className = 'protocol-tag';
    protoTag.style.cssText = 'background:var(--accent-color); color:white; padding:2px 6px; border-radius:4px; font-size:10px; flex-shrink:0;';
    protoTag.textContent = displayType;

    const nameSpan = document.createElement('span');
    nameSpan.className = 'server-name-text';
    nameSpan.textContent = displayName;

    titleH4.appendChild(protoTag);
    titleH4.appendChild(nameSpan);

    const addressP = document.createElement('p');
    addressP.style.cssText = 'font-size:11px; color:var(--text-dim); margin:2px 0 0;';
    addressP.textContent = info.address || t.unknownAddress || 'Unknown';

    infoDiv.appendChild(titleH4);
    infoDiv.appendChild(addressP);

    detailsDiv.appendChild(iconDiv);
    detailsDiv.appendChild(infoDiv);

    const pingDiv = document.createElement('div');
    pingDiv.className = 'ping';
    pingDiv.style.color = pingColor;
    pingDiv.textContent = latency;

    // Star toggle button
    const starBtn = document.createElement('button');
    starBtn.style.cssText = 'background:none; border:none; color:var(--text-dim); cursor:pointer; font-size:16px; padding:4px; display:flex; align-items:center; transition:color 0.2s;';
    const isFav = favoriteLinks && favoriteLinks.has(link);
    starBtn.innerHTML = isFav ? '★' : '☆';
    if (isFav) starBtn.style.color = '#f59e0b';
    starBtn.title = isFav
      ? (t.favoriteRemove || 'Remove from favorites')
      : (t.favoriteAdd || 'Add to favorites');

    starBtn.onclick = (e) => {
      e.stopPropagation();
      if (onToggleFavorite) onToggleFavorite(link);
    };

    const rightContainer = document.createElement('div');
    rightContainer.style.cssText = 'display:flex; align-items:center; gap:8px;';
    rightContainer.appendChild(pingDiv);
    rightContainer.appendChild(starBtn);

    card.appendChild(detailsDiv);
    card.appendChild(rightContainer);

    card.onclick = () => onServerSelect(link, displayName, displayType, info.address);
    container.appendChild(card);
  });
}

export function setSortMode(mode) { currentSortMode = mode; }
export function setPingData(link, latency) { pingData[link] = latency; }
