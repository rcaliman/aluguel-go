const CACHE_NAME='aluguel-go-v5';
const assets=['/css/style.css','/js/go-app.js','/vendor/bootstrap.min.css','/vendor/bootstrap.bundle.min.js','/vendor/jquery.min.js','/vendor/jquery.mask.min.js','/vendor/bootstrap-icons/bootstrap-icons.css','/vendor/bootstrap-icons/fonts/bootstrap-icons.woff2','/js/script.js'];
self.addEventListener('install',e=>{e.waitUntil(caches.open(CACHE_NAME).then(c=>c.addAll(assets)));self.skipWaiting()});
self.addEventListener('activate',e=>e.waitUntil(caches.keys().then(keys=>Promise.all(keys.filter(k=>k.startsWith('aluguel-')&&k!==CACHE_NAME).map(k=>caches.delete(k)))).then(()=>self.clients.claim())));
self.addEventListener('fetch',e=>{const u=new URL(e.request.url);if(e.request.method!=='GET'||u.origin!==self.location.origin||!assets.includes(u.pathname))return;e.respondWith(fetch(e.request).catch(()=>caches.match(e.request)))});
