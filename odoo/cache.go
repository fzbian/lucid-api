package odoo

import (
	"sync"
	"time"
)

// cacheEntry almacena un valor en cache con TTL
type cacheEntry struct {
	data      interface{}
	expiresAt time.Time
}

// odooCache es un cache en memoria thread-safe con TTL
var odooCache = struct {
	sync.RWMutex
	entries map[string]cacheEntry
}{entries: make(map[string]cacheEntry)}

// getCached retorna el valor cacheado si existe y no ha expirado
func getCached(key string) (interface{}, bool) {
	odooCache.RLock()
	defer odooCache.RUnlock()
	entry, ok := odooCache.entries[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.data, true
}

// setCache almacena un valor en cache con un TTL específico
func setCache(key string, data interface{}, ttl time.Duration) {
	odooCache.Lock()
	defer odooCache.Unlock()
	odooCache.entries[key] = cacheEntry{
		data:      data,
		expiresAt: time.Now().Add(ttl),
	}
}

// invalidateCache elimina una entrada específica del cache
func invalidateCache(key string) {
	odooCache.Lock()
	defer odooCache.Unlock()
	delete(odooCache.entries, key)
}

// ClearAllCache limpia todo el cache de Odoo (exportada para uso desde controllers)
func ClearAllCache() int {
	odooCache.Lock()
	defer odooCache.Unlock()
	count := len(odooCache.entries)
	odooCache.entries = make(map[string]cacheEntry)
	return count
}

// InvalidateBillingCache invalida el cache de billing para un año/mes específico
func InvalidateBillingCache(year, month int) {
	key := billingCacheKey(year, month)
	invalidateCache(key)
}
