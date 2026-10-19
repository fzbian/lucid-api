# Guía de integración Frontend – ATM API (multi-caja + cashout)

Este documento resume los cambios recientes en la API y cómo consumirla desde el frontend: qué enviar, qué devuelve y consideraciones de UI para renderizar correctamente.

## Selección dinámica de POS en facturación

- `GET /api/billing/configs?refresh=1&require_odoo=1` obtiene el catálogo actual de Odoo, sin limitarlo a nombres de locales predefinidos.
- La identidad de cada local es `odoo_pos_id` (`pos.config.id`). `pos_name` es su nombre visible actual. El nombre solo se usa para vincular configuraciones antiguas que todavía no tienen ID.
- Los POS recién descubiertos se crean desactivados para informes; los POS conocidos conservan su selección y sus costos.
- `POST /api/billing/configs` recibe `entries` con todos los POS obtenidos y su `odoo_pos_id`, `pos_name`, `include_in_reports` y costos. El guardado se verifica por ID, incluso si Odoo cambió el nombre desde que se abrió la pantalla.
- Si aparece o desaparece un POS durante la edición, el guardado devuelve 409 con el catálogo actualizado. Hay que actualizar la configuración antes de guardar.
- Al reconocer un cambio de nombre por el mismo ID, se actualizan las referencias locales de costos, gastos, informes, asignaciones de nómina y exclusiones en una transacción, conservando sus valores.
- Si Odoo falla, las lecturas pueden usar las configuraciones con ID guardadas; el guardado requiere sincronizar con Odoo.

Para publicar este cambio, desplegar primero `lucid-api` y después `lucid-front`. Luego abrir `/billing` → Configuración de Locales → actualizar → seleccionar el nuevo POS → Guardar cambios. Ambos repositorios necesitan la actualización para eliminar los filtros antiguos.

Pruebas de integración: `go test ./...` usa Odoo simulado y una base SQLite temporal; no necesita credenciales de producción.

`nomina_payments.notes` utiliza `LONGTEXT`. La migración automática de GORM crea la columna con ese tipo en instalaciones nuevas y amplía las columnas existentes al iniciar, salvo que `DB_SKIP_MIGRATIONS=1`. Si el cambio ya se aplicó directamente en MySQL, el modelo conserva el mismo tipo.

## Resumen de cambios clave

- Multi-caja en transacciones:
  - Cada transacción ahora debe asociarse a una caja mediante `caja_id` (requerido en creación y requerido en actualización – puede ir en body o query).
  - Validaciones de saldo para EGRESO se hacen contra la caja indicada.
  - Listado de transacciones agrega filtro opcional `caja_id`.
  - Notificaciones de transacciones incluyen etiqueta de caja (1 → Efectivo, 2 → Cuenta bancaria).

- Caja – saldos:
  - `GET /api/caja?solo_caja=true` devuelve ahora dos saldos locales: `saldo_caja` (id 1) y `saldo_caja2` (id 2).
  - `GET /api/caja` (modo combinado) incluye `saldo_caja`, `saldo_caja2`, saldos de Odoo (por POS), `total_locales` y `saldo_total = caja1 + caja2 + total_locales`.

- Cashout Odoo:
  - `POST /api/odoo/cashout` acepta opcionalmente `categoria_id`.
  - Siempre que el cashout en Odoo sea OK se envía un mensaje al chat “gastos” (interno del backend).
  - Si `categoria_id == 16` (Efectivos Puntos de Venta), además se crea una transacción interna local de INGRESO en `caja_id = 1` y se notifica al chat “atm” como cualquier transacción de ingreso.
  - Si `categoria_id != 16`, no se crea transacción local; solo se hace el cashout y se avisa al chat “gastos”.

- Notificaciones (internas del backend):
  - El backend enruta mensajes a chats por nombre (por defecto “atm”, adicional “gastos” para cashout). El frontend no debe llamar nada extra.

- Endpoints auxiliares:
  - `GET /api/odoo/pos` devuelve los nombres de POS disponibles para ayudar al usuario a seleccionar un POS válido antes del cashout.

## Base y headers

- Base URL: `http://localhost:<API_PORT>` (por defecto 8080).
- Headers comunes: `Content-Type: application/json`.

## Endpoints y ejemplos

### Caja

GET `/api/caja?solo_caja=true`
- Devuelve saldos locales de caja 1 y 2.
- Respuesta 200 (ejemplo):
```json
{
  "id": 1,
  "saldo_caja": 13610003,
  "saldo_caja2": 100000,
  "ultima_actualizacion": "2025-09-20T03:18:06-05:00"
}
```

GET `/api/caja`
- Devuelve saldos locales + saldos agregados por POS en Odoo.
- Respuesta 200 (ejemplo):
```json
{
  "id": 1,
  "saldo_caja": 13610003,
  "saldo_caja2": 100000,
  "locales": {
    "POS_Local_1": 250000,
    "POS_Local_2": 375000
  },
  "total_locales": 625000,
  "saldo_total": 14335003,
  "ultima_actualizacion": "2025-09-25T10:24:37-05:00"
}
```

UI tip: mostrar etiqueta de caja
- 1 → “Efectivo”
- 2 → “Cuenta bancaria”

---

### Transacciones

GET `/api/transacciones`
- Filtros (opcionales):
  - `limit`, `from` (RFC3339/`YYYY-MM-DD`), `to` (RFC3339/`YYYY-MM-DD`), `tipo` (INGRESO|EGRESO), `descripcion`, `usuario`, `caja_id`.
- Respuesta 200 (ejemplo):
```json
[
  {
    "id": 101,
    "categoria_id": 3,
    "caja_id": 1,
    "monto": 50000,
    "fecha": "2025-09-25T10:20:00-05:00",
    "descripcion": "Venta mostrador",
    "usuario": "fabian"
  }
]
```

POST `/api/transacciones`
- Crea una transacción.
- Body (todos required):
```json
{
  "categoria_id": 5,
  "caja_id": 2,
  "monto": 120000,
  "descripcion": "Pago proveedor",
  "usuario": "fabian"
}
```
- Respuesta 201 (ejemplo):
```json
{
  "id": 102,
  "categoria_id": 5,
  "caja_id": 2,
  "monto": 120000,
  "fecha": "2025-09-25T10:23:00-05:00",
  "descripcion": "Pago proveedor",
  "usuario": "fabian"
}
```
- Error 409 (saldo insuficiente):
```json
{
  "error": "Saldo insuficiente en la caja para realizar el egreso",
  "saldo_actual": 80000,
  "monto_solicitado": 120000,
  "caja_id": 2
}
```

PUT `/api/transacciones/{id}`
- Actualiza parcialmente. Requiere `usuario` (query) y `caja_id` (en body o query).
- Body (opcionales; solo se actualiza lo enviado): `categoria_id`, `caja_id`, `monto`, `descripcion`.
- Validaciones EGRESO:
  - Si cambia de caja: la caja destino debe tener saldo ≥ `monto`.
  - Misma caja y aumento de `monto`: la caja debe tener saldo ≥ incremento.
- Respuesta 200 (ejemplo):
```json
{
  "id": 102,
  "categoria_id": 5,
  "caja_id": 2,
  "monto": 100000,
  "fecha": "2025-09-25T10:23:00-05:00",
  "descripcion": "Pago proveedor actualizado",
  "usuario": "fabian"
}
```

DELETE `/api/transacciones/{id}`
- Requiere `usuario` (query). Respuesta 204 sin cuerpo.

---

### Odoo – POS y Cashout

GET `/api/odoo/pos`
- Devuelve nombres de POS disponibles (array de strings) para autocompletar.

POST `/api/odoo/cashout`
- Realiza un cashout en Odoo.
- Body (required + opcional):
```json
{
  "pos_name": "POS_Centro",
  "amount": 50000,
  "category_name": "RETIRADA",
  "reason": "Traslado a caja central",
  "usuario": "fabian",
  "categoria_id": 16
}
```
- Reglas de negocio:
  - Siempre que el cashout sea OK:
    - Se envía un mensaje al chat “gastos” (interno backend) con POS, monto y razón.
  - Si `categoria_id == 16` (Efectivos Puntos de Venta):
    - Se crea transacción local de INGRESO con `categoria_id=16`, `caja_id=1`, `monto=amount`, `descripcion="<POS sin _>: <reason>"`, `usuario`.
    - Se envía la notificación estándar de transacción al chat “atm”.
  - Si `categoria_id != 16`: no se crea transacción local (solo el mensaje a “gastos”).
- Respuesta 200 (éxito típico):
```json
{
  "ok": true,
  "message": "Cash out OK sesión 123 monto 50000",
  "session_id": 123
}
```
- Errores comunes:
  - 400: validaciones de payload (p. ej. `reason` vacío, `pos_name` no encontrado – se retorna lista disponible).
  - 500: errores con Odoo/entorno (autenticación, JSON-RPC, etc.).

---

### Notify (solo referencia)
- El frontend no consume estos endpoints directamente; el backend envía mensajes a chats:
  - “atm”: notificaciones de transacciones.
  - “gastos”: notificación de retiros (cashout).

## Errores y códigos de estado
- Formato genérico de error:
```json
{ "error": "mensaje", "detalle": "opcional" }
```
- Códigos típicos: 200 OK, 201 Created, 204 No Content, 400 Bad Request, 404 Not Found, 409 Conflict, 500 Internal Server Error.

## Swagger
- UI: `GET /swagger/index.html`
- JSON: `GET /swagger/doc.json`

## Sugerencias de UI
- Mapear `caja_id` a etiqueta amigable:
  - 1 → “Efectivo”
  - 2 → “Cuenta bancaria”
- Formateo de monto: miles con punto, decimales con coma (ej. `12.345,67`).
- En cashout: 
  - ofrecer autocompletado de POS con `GET /api/odoo/pos`;
  - en caso de categoría “Efectivos Puntos de Venta” usar `categoria_id = 16` para disparar el ingreso local.

## Checklist para el Frontend
- [ ] Añadir `caja_id` en creación y actualización de transacciones.
- [ ] Permitir filtrar por `caja_id` en listados.
- [ ] Mostrar `saldo_caja2` en el widget de caja cuando `solo_caja=true`.
- [ ] Enviar `categoria_id` en cashout cuando sea “Efectivos Puntos de Venta” (=16).
- [ ] Manejar correctamente códigos 400/409 en validaciones (mostrar saldo actual/incremento requerido si aplica).

## Deploy en Coolify (Docker)

Este backend ya incluye `Dockerfile` para despliegue directo en Coolify.

- Build Pack: `Dockerfile`
- Puerto interno del contenedor: `8080`
- Healthcheck: `GET /`

Variables de entorno requeridas en Coolify:

```env
DB_URI=DB_URI
MYSQL_URI=MYSQL_URI
API_PORT=8080
DB_LOG_LEVEL=warn
DB_SLOW_SQL_MS=2000

ODOO_URL=ODOO_URL
ODOO_DB=ODOO_DB
ODOO_USER=ODOO_USER
ODOO_PASSWORD=ODOO_PASSWORD

NOTIFY_URL=https://noti.chinatownlogistic.com
NOTIFY_APIKEY=replace_with_notify_apikey
NOTIFY_NUMBER_URL=https://noti.chinatownlogistic.com
```

Notas:
- El backend escucha por `API_PORT` (default `8080`). Si `API_PORT` no está definido, usa `PORT` y luego `8080`.
- Puedes definir solo `DB_URI`; `MYSQL_URI` se mantiene como fallback de compatibilidad.

## Cierres del día

La nueva vista `/closings` consume `GET /api/odoo/closings?date=YYYY-MM-DD` y consulta directamente el histórico de Odoo. No necesita importar PDFs ni ejecutar el bot de WhatsApp. Publicar primero la API y después el frontend; utiliza las variables Odoo existentes (`ODOO_URL`, `ODOO_DB`, `ODOO_USER`, `ODOO_PASSWORD`).

El día se determina por `pos.session.stop_at` en Colombia (UTC-5), con intervalo de medianoche inclusiva a medianoche siguiente exclusiva y estado `closed`. Cada reporte incluye toda la sesión, aunque se haya abierto otro día; el resumen representa ventas de sesiones cerradas ese día, no ventas filtradas por `date_order`. El botón Actualizar consulta los cierres nuevos.

Se consultan sesiones, órdenes pagadas/finalizadas/facturadas, líneas, pagos y movimientos de caja mediante `search_read`, con paginación completa y consultas por lote. Los errores impiden devolver un reporte parcial como completo. El usuario técnico necesita permisos de lectura sobre `pos.session`, `pos.order`, `pos.order.line`, `pos.payment` y `account.bank.statement.line`.

El PDF se genera en el navegador con jsPDF (ya presente en Lucid), sin depender del servicio anterior. Incluye un resumen ejecutivo con ventas netas, operaciones y devoluciones; medios de pago con participación neta; cuadre de caja con alertas de faltantes o sobrantes; productos vendidos; movimientos de efectivo; y detalle de ventas y reembolsos. El diseño utiliza encabezados de Lucid, tablas sin cuadrícula pesada, cifras alineadas, fecha completa en hora de Colombia y pies de página con sesión y numeración. Los saltos de página mantienen juntas las operaciones cortas y repiten encabezados de tablas extensas. Los subtotales de productos se muestran sin impuestos, como en el reporte original. Las cifras se presentan en COP. Los movimientos manuales conservan el criterio original de `payment_ref` con `POS/` y `-`, excluyendo asientos automáticos de ventas.

Verificación: `go test ./...` incluye pruebas de límites de fecha, paginación, devoluciones, cambio de efectivo, errores de Odoo y días sin cierres, con Odoo simulado. El módulo no modifica sesiones ni envía mensajes.

## Horarios POS: horas de apertura

La vista `/pos-sessions` permite consultar día, semana (lunes a domingo), mes o rango personalizado de hasta 366 días; filtrar locales; ordenar por horas o nombre; y agrupar por local o por día. El rango personalizado se aplica con **Consultar rango**. La selección se conserva en la URL. No reutiliza las antiguas preferencias que ocultaban puntos de venta.

`GET /api/odoo/pos-sessions?from=YYYY-MM-DD&to=YYYY-MM-DD` interpreta ambas fechas en Colombia; `to` incluye todo el día seleccionado. También acepta RFC3339, cuyo límite superior es exclusivo. Sin fechas consulta la semana actual hasta hoy. No incluye días futuros. Publicar primero la API y después el frontend: la nueva vista necesita el desglose `days`.

El catálogo se consulta directamente en `pos.config`, sin caché ni filtros de facturación, con paginación y `active_test=false`. Se muestran todos los locales activos, aunque no tengan sesiones, y locales archivados cuando tienen sesiones relevantes. Las sesiones se consultan por intersección con el rango, incluyendo las abiertas antes del inicio. Se leen todas las páginas; `limit` antiguo no recorta los resultados. Un error de Odoo devuelve 502, sin presentar resultados incompletos como completos.

Las horas confirmadas son la unión de intervalos de sesiones cerradas con fechas válidas. Se excluyen pausas y se cuentan una sola vez los solapamientos. Una sesión que cruza medianoche aporta a cada día su parte, en UTC-5. El resumen diario muestra 00:00/24:00 con indicación de continuidad; su detalle conserva las fechas y horas reales de la sesión. Las sesiones sin cierre o con fechas inválidas se señalan para revisión y no suman duración estimada. Una duración válida de cero minutos se distingue de un día sin sesiones.

El promedio diario usa solo los días con sesiones cerradas y sin pendientes. Las horas confirmadas de días parcialmente pendientes sí se incluyen en el total, pero esos días no diluyen el promedio. Los totales entre locales suman horas de cada punto; no representan asistencia de una persona. El módulo es de solo lectura y muestra el responsable registrado en cada sesión sin atribuirle una jornada laboral.

Pruebas: `go test ./...` cubre fronteras de fecha, turnos nocturnos, pausas, solapamientos, pendientes, ceros válidos, promedios, paginación de locales/sesiones y fallos. En el frontend, ejecutar React Scripts directamente: `CI=true node node_modules/react-scripts/bin/react-scripts.js test --watchAll=false --runInBand --runTestsByPath src/posHours.test.js` (el script npm `test` del proyecto está deshabilitado).
