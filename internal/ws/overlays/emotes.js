(function() {
  const twitchEmoteCDN = 'https://static-cdn.jtvnw.net/emoticons/v2/';

  function normalizeEmoteURL(raw) {
    const u = (raw || '').trim();
    if (!u) return '';
    if (u.indexOf(twitchEmoteCDN) === 0) {
      return u.replace('/static/', '/default/');
    }
    return u;
  }

  function resolveEmoteURLs(sp, platform) {
    let base = normalizeEmoteURL(sp && sp.url);
    if (!base && sp && sp.id) {
      if (platform === 'twitch') {
        base = twitchEmoteCDN + encodeURIComponent(sp.id) + '/default/dark/2.0';
      } else if (platform === 'kick') {
        base = 'https://files.kick.com/emotes/' + encodeURIComponent(sp.id) + '/fullsize';
      }
    }
    if (!base) return { primary: '', fallback: '' };
    if (base.indexOf(twitchEmoteCDN) === 0 && /\/1\.0(\?.*)?$/.test(base)) {
      return {
        primary: base.replace(/\/1\.0(\?.*)?$/, '/2.0$1'),
        fallback: base
      };
    }
    if (base.indexOf(twitchEmoteCDN) === 0 && /\/2\.0(\?.*)?$/.test(base)) {
      return {
        primary: base,
        fallback: base.replace(/\/2\.0(\?.*)?$/, '/1.0$1')
      };
    }
    return { primary: base, fallback: '' };
  }

  function buildEmoteNode(sp, altText, platform) {
    const urls = resolveEmoteURLs(sp, platform);
    if (!urls.primary) {
      return document.createTextNode(altText);
    }
    const img = document.createElement('img');
    img.className = 'emote';
    img.alt = altText;
    img.title = altText;
    img.loading = 'lazy';
    img.decoding = 'async';
    let retriedFallback = false;
    img.onerror = function() {
      if (!retriedFallback && urls.fallback && urls.fallback !== img.src) {
        retriedFallback = true;
        img.src = urls.fallback;
        return;
      }
      const target = (img.parentNode && img.parentNode.classList && img.parentNode.classList.contains('cheermote'))
        ? img.parentNode
        : img;
      if (target.parentNode) {
        target.parentNode.replaceChild(document.createTextNode(altText), target);
      }
    };
    img.src = urls.primary;
    if (sp && sp.bits > 0) {
      const wrap = document.createElement('span');
      wrap.className = 'cheermote';
      wrap.appendChild(img);
      const lbl = document.createElement('span');
      lbl.className = 'cheermote-bits';
      lbl.textContent = String(sp.bits);
      if (sp.color && /^#[0-9a-fA-F]{3,8}$/.test(sp.color)) {
        lbl.style.color = sp.color;
      }
      wrap.appendChild(lbl);
      return wrap;
    }
    return img;
  }

  function renderTextWithEmotes(container, text, emotes, platform) {
    container.textContent = '';
    const str = text || '';
    if (!str) return;
    if (!Array.isArray(emotes) || emotes.length === 0) {
      container.textContent = str;
      return;
    }
    const runes = Array.from(str);
    const sorted = emotes.slice().sort(function(a, b) {
      return (a.start || 0) - (b.start || 0);
    });
    let pos = 0;
    for (let i = 0; i < sorted.length; i++) {
      const sp = sorted[i];
      if (!sp || typeof sp.start !== 'number' || typeof sp.end !== 'number') continue;
      if (sp.start < pos || sp.start >= runes.length || sp.end <= sp.start) continue;
      const end = Math.min(sp.end, runes.length);
      if (sp.start > pos) {
        container.appendChild(document.createTextNode(runes.slice(pos, sp.start).join('')));
      }
      const sliceText = runes.slice(sp.start, end).join('');
      container.appendChild(buildEmoteNode(sp, sp.name || sliceText, platform));
      pos = end;
    }
    if (pos < runes.length) {
      container.appendChild(document.createTextNode(runes.slice(pos).join('')));
    }
  }

  function renderTranslatedWithEmotes(container, translatedText, emotes, platform) {
    container.textContent = '';
    const str = translatedText || '';
    if (!str) return;
    if (!Array.isArray(emotes) || emotes.length === 0) {
      container.textContent = str;
      return;
    }
    const byName = Object.create(null);
    for (let i = 0; i < emotes.length; i++) {
      const sp = emotes[i];
      if (sp && sp.name && !Object.prototype.hasOwnProperty.call(byName, sp.name)) {
        byName[sp.name] = sp;
      }
    }
    if (Object.keys(byName).length === 0) {
      container.textContent = str;
      return;
    }
    const parts = str.split(/(\s+)/);
    for (let i = 0; i < parts.length; i++) {
      const part = parts[i];
      if (!part) continue;
      if (Object.prototype.hasOwnProperty.call(byName, part)) {
        container.appendChild(buildEmoteNode(byName[part], part, platform));
      } else {
        container.appendChild(document.createTextNode(part));
      }
    }
  }

  window.TownCrierEmotes = {
    normalizeEmoteURL: normalizeEmoteURL,
    resolveEmoteURLs: resolveEmoteURLs,
    buildEmoteNode: buildEmoteNode,
    renderTextWithEmotes: renderTextWithEmotes,
    renderTranslatedWithEmotes: renderTranslatedWithEmotes
  };
})();
