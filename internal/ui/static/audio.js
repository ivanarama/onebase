// Optional managed/auto-form player. No metadata or media request on startup.
(function () {
  'use strict';
  if (window.obAudioApply) return;
  var roots = Array.prototype.slice.call(document.querySelectorAll('[data-ob-audio]'));
  if (!roots.length) return;
  var generation = 0;
  var uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
  function source(value) {
    value = String(value || '').trim();
    return uuid.test(value) && value !== '00000000-0000-0000-0000-000000000000' ? value.toLowerCase() : '';
  }
  function value(values, key) {
    if (!values || !key) return undefined;
    var found = Object.keys(values).find(function (k) { return k.toLowerCase() === key.toLowerCase(); });
    return found === undefined ? undefined : values[found];
  }
  function removeValue(values, key) {
    if (!values || !key) return;
    Object.keys(values).forEach(function (k) { if (k.toLowerCase() === key.toLowerCase()) delete values[k]; });
  }
  function status(p, text) { p.status.textContent = text || ''; p.status.title = text || ''; }
  function clock(seconds) {
    seconds = Number.isFinite(seconds) && seconds > 0 ? Math.floor(seconds) : 0;
    var minutes = Math.floor(seconds / 60), tail = String(seconds % 60).padStart(2, '0');
    return minutes + ':' + tail;
  }
  function sync(p) {
    var duration = Number.isFinite(p.media.duration) && p.media.duration > 0 ? p.media.duration : 0;
    if (p.time) p.time.textContent = clock(p.media.currentTime) + ' / ' + clock(duration);
    p.play.textContent = p.media.paused ? '▶' : '⏸';
    var title = p.root.getAttribute(p.media.paused ? 'data-audio-play-label' : 'data-audio-pause-label');
    if (title) { p.play.title = title; if (p.play.setAttribute) p.play.setAttribute('aria-label', title); }
    if (p.seek) {
      var percent = duration ? Math.max(0, Math.min(1, p.media.currentTime / duration)) : 0;
      p.seek.setAttribute('aria-valuenow', String(Math.round(percent * 100)));
      p.seek.setAttribute('aria-valuetext', clock(p.media.currentTime) + ' / ' + clock(duration));
      p.seek.setAttribute('aria-disabled', String(!duration || p.blocked));
      p.seek.style.setProperty('--ob-audio-progress', (percent * 100) + '%');
    }
    if (p.volume) {
      var level = p.media.muted ? 0 : p.media.volume;
      p.volume.setAttribute('aria-valuenow', String(Math.round(level * 100)));
      p.volume.title = p.volume.getAttribute('aria-label') + ': ' + Math.round(level * 100) + '%';
      p.volume.style.setProperty('--ob-audio-progress', (level * 100) + '%');
    }
  }
  // Sliders emit no form input/change events and have no name/value carrier.
  // Seeking and volume must never mark the business object as locally edited.
  function slider(element, read, write, enabled) {
    if (!element) return;
    function point(e) {
      if (!enabled()) return;
      var rect = element.getBoundingClientRect();
      if (rect.width) write(Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width)));
    }
    element.addEventListener('pointerdown', function (e) {
      if (e.button !== 0 || !enabled()) return;
      e.preventDefault(); element.focus(); element.setPointerCapture(e.pointerId); point(e);
    });
    element.addEventListener('pointermove', function (e) { if (element.hasPointerCapture(e.pointerId)) point(e); });
    function release(e) { if (element.hasPointerCapture(e.pointerId)) element.releasePointerCapture(e.pointerId); }
    element.addEventListener('pointerup', release); element.addEventListener('pointercancel', release);
    element.addEventListener('keydown', function (e) {
      if (!enabled()) return;
      var next = read();
      if (e.key === 'ArrowRight' || e.key === 'ArrowUp') next += 0.05;
      else if (e.key === 'ArrowLeft' || e.key === 'ArrowDown') next -= 0.05;
      else if (e.key === 'Home') next = 0;
      else if (e.key === 'End') next = 1;
      else return;
      e.preventDefault(); e.stopPropagation(); write(Math.max(0, Math.min(1, next)));
    });
  }
  function stop(p) {
    p.ticket++;
    p.waiting = false;
    p.media.pause();
    try { p.media.currentTime = 0; } catch (_) {}
    // Abort active media transfers, but keep the prepared attachment identity.
    p.media.removeAttribute('src');
    p.media.load();
    sync(p);
  }
  function disabled(p) {
    return p.blocked || p.play.disabled || p.waiting || p.root.style.display === 'none' || !!p.root.closest('[hidden],fieldset[disabled]');
  }
  function supported(a) {
    var mime = String(a.mime_type || '').toLowerCase().split(';')[0].trim();
    if (/^audio\/(ogg|opus|mpeg|mp3|wav|wave|x-wav)$/.test(mime)) return true;
    return (!mime || mime === 'application/octet-stream' || mime === 'application/ogg') && /\.(ogg|oga|opus|mp3|wav)$/i.test(a.filename || '');
  }
  var players = roots.map(function (root) {
    var p = {
      root: root, media: root.querySelector('[data-ob-audio-media]'), play: root.querySelector('[data-ob-audio-play]'),
      caption: root.querySelector('[data-ob-audio-caption]'), status: root.querySelector('[data-ob-audio-status]'),
      time: root.querySelector('[data-ob-audio-time]'), seek: root.querySelector('[data-ob-audio-seek]'), volume: root.querySelector('[data-ob-audio-volume]'),
      name: root.getAttribute('data-audio-name'), sourceField: root.getAttribute('data-audio-source-field'),
      titleField: root.getAttribute('data-audio-title-field'), id: source(root.getAttribute('data-audio-source')),
      metadataURL: root.getAttribute('data-audio-metadata-url') || '',
      prepare: !!root.getAttribute('data-audio-play-event'), ticket: 0, waiting: false, blocked: false
    };
    p.media.preload = 'none';
    ['loadedmetadata', 'durationchange', 'timeupdate', 'play', 'pause', 'ended', 'emptied', 'volumechange'].forEach(function (event) {
      p.media.addEventListener(event, function () { sync(p); });
    });
    slider(p.seek, function () { return p.media.currentTime / p.media.duration; }, function (fraction) {
      p.media.currentTime = fraction * p.media.duration; sync(p);
    }, function () { return !p.blocked && !!p.media.getAttribute('src') && Number.isFinite(p.media.duration) && p.media.duration > 0; });
    slider(p.volume, function () { return p.media.muted ? 0 : p.media.volume; }, function (fraction) {
      p.media.volume = fraction; p.media.muted = false; sync(p);
    }, function () { return true; });
    sync(p);
    root.querySelector('[data-ob-audio-stop]').addEventListener('click', function () { stop(p); status(p, ''); });
    p.media.addEventListener('error', function () { if (p.media.getAttribute('src')) status(p, root.getAttribute('data-audio-error')); });
    p.play.addEventListener('click', async function () {
      if (disabled(p)) return;
      if (p.media.getAttribute('src') && !p.media.paused) { p.media.pause(); sync(p); return; }
      var epoch = generation, ticket = ++p.ticket;
      function current() { return generation === epoch && ticket === p.ticket && !p.blocked && !disabled(p) && root.isConnected; }
      try {
        if (!p.id) {
          p.waiting = true;
          status(p, root.getAttribute('data-audio-loading'));
          if (p.prepare) {
            if (typeof window.obFire !== 'function') throw new Error('form-event unavailable');
            await window.obFire(p.name, 'Воспроизведение');
          } else if (p.metadataURL) {
            // Only a canonical same-origin authenticated attachment list is allowed.
            if (!/^\/ui\/[^/?#]+\/[^/?#]+\/[0-9a-f-]{36}\/attachments$/i.test(p.metadataURL)) throw new Error('invalid attachment list');
            var res = await fetch(p.metadataURL, {credentials: 'same-origin', headers: {'Accept': 'application/json'}});
            if (!res.ok) throw new Error('attachment list denied');
            var items = await res.json();
            if (epoch !== generation || ticket !== p.ticket || !root.isConnected) return;
            if (!Array.isArray(items)) throw new Error('invalid attachment list');
            var audio = items.find(function (a) { return a && source(a.id) && supported(a); });
            if (audio) p.id = source(audio.id);
          }
          p.waiting = false;
          if (!current()) return;
          if (!p.id) { status(p, root.getAttribute('data-audio-empty')); return; }
        }
        if (!current()) return;
        players.forEach(function (other) { if (other !== p) stop(other); });
        var url = '/ui/attachments/' + p.id + '/download';
        if (p.media.getAttribute('src') !== url) p.media.setAttribute('src', url);
        await p.media.play();
        sync(p);
        if (!current()) { p.media.pause(); return; }
        status(p, '');
      } catch (error) {
        if (epoch === generation && ticket === p.ticket) {
          // Native Pause can cancel a still-pending play(). It leaves no
          // MediaError; actual media/policy failures must remain visible.
          var paused = error && error.name === 'AbortError' && p.media.paused && !p.media.error;
          status(p, paused ? '' : root.getAttribute('data-audio-error'));
        }
      } finally {
        if (ticket === p.ticket) p.waiting = false;
      }
    });
    return p;
  });
  window.obAudioBeforeEvent = function (name, event, extra) {
    var result = Object.assign({}, extra || {});
    if (event === 'ПриАктивизацииСтроки') {
      generation++;
      players.forEach(function (p) {
        stop(p); p.id = ''; p.waiting = false; p.blocked = true;
        status(p, p.root.getAttribute('data-audio-loading'));
      });
    }
    result._ob_audio_generation = String(generation);
    return result;
  };
  window.obAudioFilterResponse = function (data, snapshot) {
    var sent = snapshot.extraParams && snapshot.extraParams._ob_audio_generation;
    var stale = sent !== undefined && String(sent) !== String(generation);
    data._obAudioPreparing = !stale && data.ok && !data.error && snapshot.eventName === 'Воспроизведение' ? snapshot.elementName : '';
    players.forEach(function (p) {
      if (stale || !data.ok || data.error) {
        removeValue(data.values, p.sourceField);
        if (stale) {
          removeValue(data.values, p.titleField);
          var path = p.root.getAttribute('data-ob-el-path');
          if (data.elementStates) {
            ['readonly', 'hidden'].forEach(function (key) { if (data.elementStates[key]) delete data.elementStates[key][path]; });
          }
        }
      }
    });
    if (!stale && snapshot.eventName === 'ПриАктивизацииСтроки') players.forEach(function (p) { p.blocked = false; status(p, ''); });
  };
  window.obAudioApply = function (data) {
    players.forEach(function (p) {
      var id = value(data.values, p.sourceField), title = value(data.values, p.titleField);
      if (id !== undefined) {
        var next = source(id);
        if (p.id !== next) {
          // Preparation may change empty -> attachment while its Play awaits.
          // Stop old media without invalidating that same in-flight user intent.
          var ticket = p.ticket, keepIntent = p.waiting && data._obAudioPreparing === p.name;
          stop(p); if (keepIntent) p.ticket = ticket;
          p.id = next;
        }
      }
      if (title !== undefined) { p.caption.textContent = String(title || ''); p.caption.title = String(title || ''); }
      if ((data.audioStops || []).some(function (name) { return String(name).toLowerCase() === p.name.toLowerCase(); })) stop(p);
      if (disabled(p) && p.media.getAttribute('src')) stop(p);
    });
  };
  // A duplicate/invalid grid activation can be discarded before HTTP. It still
  // settles obFire's promise, so release this generation's playback lock too.
  var originalFire = window.obFire;
  if (typeof originalFire === 'function') {
    window.obFire = function (name, event) {
      var result = originalFire.apply(this, arguments), epoch = generation;
      if (event !== 'ПриАктивизацииСтроки') return result;
      return Promise.resolve(result).finally(function () {
        if (epoch === generation) players.forEach(function (p) {
          if (p.blocked) { p.blocked = false; status(p, ''); }
        });
      });
    };
  }
  window.addEventListener('pagehide', function () { players.forEach(stop); });
}());
