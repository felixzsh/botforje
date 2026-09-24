Plan de implementación: publicación de configuración por API — Phase 7
Contexto para retomar en una sesión nueva
Trabajar solo en main de /home/felix/Work/personal/botflota. Al preparar este plan, el árbol estaba limpio y HEAD era 5c14dd7; comprobar de nuevo el estado antes de editar. Leer docs/glossary.md, PLAN.md y los ADR 0002, 0009 y 0016.
Botflota usa YAML como única fuente canónica de comportamiento. Un proceso ejecuta bots de varios scopes. Hay dos autoridades: owner puede operar cualquier scope y la instancia; un token scoped puede leer y modificar únicamente sus scopes. No hay roles ni se planean. La UI futura accederá exclusivamente por API. No comenzar Phase 8 ni implementar la UI.
Estado técnico relevante:
- GET /api/v1/scopes/{scope}/config devuelve hoy la configuración activa; no existe escritura por API.
- config.Loader carga el principal e include anidados, combina entidades de un mismo scope y registra su archivo de origen por (tipo, scope, nombre). Distintas entidades del mismo scope pueden vivir en archivos diferentes; varios scopes pueden compartir un archivo.
- El watcher en cmd/botflota/reload.go carga y valida YAML; config.Source guarda el snapshot activo; fleet.ApplyConfig activa cambios seguros por scope y deja otros pendientes. YAML guardado y runtime activo pueden diferir.
- PLAN.md:258–277 deja pendiente la publicación por API. docs/api.md:55–90 especifica versiones, aislamiento y concurrencia, pero aún no existe el publicador.
- Contradicción que hay que resolver antes de programar: ADR 0002 y PLAN.md exigen editar definiciones en su archivo de origen, incluso si viven en config.yml, pero también dicen que la API nunca reescribe el principal. Para cumplir edición de scopes existentes sin exigir reorganización manual, actualizar la decisión canónica: permitir cambios acotados a scopes.<scope> y, cuando haga falta, el registro de un include en el principal; preservar settings globales y otros scopes. El formato o los comentarios de un archivo reescrito no necesitan permanecer idénticos.
Contrato a entregar
1. La API publica un scope lógico, sin que el cliente conozca rutas de archivos. El owner puede publicar cualquiera; un token scoped solo los suyos. Archivos y entidades ajenos nunca aparecen en sus respuestas.
2. GET entrega la versión guardada del scope, una versión opaca para editar y el estado/versión activa. No usar el snapshot activo como base de una edición cuando hay YAML válido guardado pero pendiente.
3. Una operación de publicación —por ejemplo PUT /api/v1/scopes/{scope}/config con versión esperada— modifica únicamente ese scope. Las entidades existentes conservan su archivo de origen; las nuevas van a un fragmento administrado y alcanzable por include. Si hay que registrar ese include en el principal, hacerlo dentro de la misma publicación.
4. Dos cambios de scopes distintos en el mismo archivo deben conservarse ambos: al confirmar, releer el archivo más reciente y modificar solo el subtree autorizado. Dos cambios al mismo scope basados en una versión antigua producen conflicto. Repetir contenido idéntico es un no-op.
5. Validar el candidato completo con el loader y las mismas reglas que config validate antes de tocar archivos definitivos. Una propuesta inválida no escribe nada. Tras un guardado válido, responder diferenciando active de pending con su motivo; no prometer activación inmediata de cambios que el runtime difiere.
6. Los errores propios del scope son detallados para su token; errores globales o de otro scope son genéricos para él y diagnosticables por el owner. No filtrar rutas, nombres ajenos ni secretos.
7. No garantizar coordinación con editores manuales externos. El watcher conserva el último runtime válido y no hace undo. Si el YAML manual existente impide cargar el documento global, rechazar la publicación API sin escribir y dar al token ajeno un error genérico hasta que el owner lo corrija.
Orden de implementación
1. Alinear decisiones y fases. Corregir ADR 0002, docs/api.md y la casilla de Phase 7 en PLAN.md para resolver la contradicción del archivo principal. Quitar de ROADMAP.md Phases 14–15 la duplicación de «implementar publicación API»: allí quedan provisioning y UI, que son capacidades posteriores. Mantener la casilla de Phase 7 abierta hasta que sus pruebas pasen.
2. Construir el publicador en el dueño de los documentos, config. Exponer internamente origen y versión lógica de cada entidad/scope sin exponer rutas por API. Editar YAML en el archivo de origen —incluido el principal cuando corresponda— preservando los valores de otras entidades. Usar la dependencia YAML existente; no añadir otra. Resolver de manera determinista dónde se escriben entidades nuevas y cómo se incorpora su fragmento al árbol de includes.
3. Validación previa y secretos. Preparar una vista candidata de los archivos modificados para pasarla por el loader y la validación completa sin publicarla todavía. Los Secret de spec en GET pueden aparecer como *** cuando provienen de un literal: una edición que los deje intactos debe conservar su valor original desde el YAML, nunca escribir *** como secreto. Las sustituciones explícitas siguen la política ${ENV_VAR} de ADR 0014.
4. Commit concurrente y recuperación. Coordinar por archivo físico y comprobar la versión lógica del scope bajo el lock de publicación. Releer y revalidar después de otro commit; un cambio ajeno en el mismo archivo no convierte en obsoleta la edición del scope intacto. Acotar peticiones en espera, sin cola duradera ilimitada. Para una publicación que afecta varios archivos, implementar staging y una transacción de archivos recuperable ante error o caída: renames individuales no son atómicos como conjunto. Bloquear al watcher mientras se confirma; recuperar cualquier transacción incompleta antes de la carga/validación normal al arrancar. Hoy runFleet carga config antes de tomar el lock de instancia: ajustar ese orden o establecer un lock/journal de publicación localizable desde la ruta estable del config. No afirmar «todo-o-nada» tras un crash sin probar esa recuperación.
5. Compartir el circuito de activación. Extraer de cmd/botflota/reload.go la coordinación reutilizable cargar → validar → comparar → aplicar, sin crear un ciclo de imports entre config y fleet. Inyectarla en api.Options mediante una interfaz pequeña; el watcher y el handler usan el mismo publicador. Tras confirmar YAML, activar los scopes seguros con el mecanismo existente; mantener los incompatibles pendientes y no modificar runtime ajeno. Evitar que el watcher procese como edición nueva los renames intermedios de una publicación API.
6. Añadir la ruta y respuestas. Autorizar con scopeParam/Principal.Allows. Decodificar con límite de tamaño y exigir versión esperada. Responder con versión guardada, aplicada y estado; conflicto por edición obsoleta; errores de validación filtrados por autoridad. Distinguir edición del scope de operaciones globales, que siguen reservadas al owner y fuera de esta entrega.
Pruebas de aceptación obligatorias
- Owner, token propio y token fuera de scope: lectura/escritura permitidas y 404 sin inferencia ajena.
- Scope distribuido entre config.yml e include anidados: cambiar, crear y eliminar entidades sin alterar otras definiciones.
- Dos publicaciones simultáneas de scopes distintos en el mismo archivo: sobreviven ambas; dos del mismo scope: una obtiene conflicto.
- Edición inválida: ningún archivo cambia; error propio visible, error ajeno opaco.
- Secreto enmascarado en GET: un guardado sin cambiarlo conserva el secreto real; ninguna respuesta ni log lo revela.
- Fallo inyectado después del primer reemplazo de una publicación multiarchivo: recuperación deja toda la versión anterior o toda la nueva, también al reiniciar.
- Cambio válido pero incompatible con sesiones: YAML queda guardado, API dice pending, runtime anterior permanece y otros scopes siguen activos.
- Edición idéntica: no reescribe ni incrementa generaciones. Includes nuevos y cambios manuales siguen entrando por el watcher.
Verificar con los checks pertinentes (make check y make test-race para concurrencia), solicitando primero permiso de build según las instrucciones locales. Marcar entregables completados en el mismo commit que los complete y usar Conventional Commits.


Operación	Propósito
GET /api/v1/scopes/{scope}/config	Leer el scope guardado, su versión y si coincide con lo activo.
PUT /api/v1/scopes/{scope}/config	Reemplazar el scope lógico completo con control de versión; el servidor resuelve los archivos.
Lectura
GET /api/v1/scopes/support/config
Authorization: Bearer …
200 OK
ETag: "scope-version-opaque"
Content-Type: application/json
{
  "scope": "support",
  "config": {
    "actions": {},
    "graphs": {},
    "bots": {},
    "llms": {}
  },
  "version": "scope-version-opaque",
  "active_version": "previous-version-opaque",
  "state": "pending",
  "pending_reasons": ["graph change waits for active sessions"]
}
config es la versión guardada en YAML; active_version permite saber si el runtime aún usa otra. Para un token scoped, los motivos solo pueden describir su propio scope. Ni rutas de archivos ni otros scopes aparecen en la respuesta.
Publicación
PUT /api/v1/scopes/support/config
Authorization: Bearer …
If-Match: "scope-version-opaque"
Content-Type: application/json
{
  "config": {
    "actions": {},
    "graphs": {},
    "bots": {},
    "llms": {}
  }
}
Es un reemplazo completo del contenido lógico de ese scope: una entidad omitida se elimina. El scope se toma de la URL, no del cuerpo. Internamente la API edita las entidades en sus archivos de origen, incluso si varias comparten archivo con otro scope.
La respuesta usa la misma forma que GET:
- 200: guardado y activo, o contenido idéntico (no-op, misma versión).
- 202: guardado válidamente, pero pendiente de activación por la regla de recarga aplicable.
- 428: falta If-Match; 412: cambió ese mismo scope desde la lectura. Un cambio de otro scope en el mismo archivo no causa 412: el servidor incorpora ambos.
- 422: propuesta inválida; no se escribió nada. Devuelve detalles de entidades y campos del scope propio.
- 503 con motivo genérico para un token scoped si un YAML manual inválido fuera de su autoridad impide cargar el documento. El owner puede obtener el diagnóstico completo.
- 404 para scope desconocido o no autorizado, como ahora; 401 para credencial inválida.
Secretos: un GET puede mostrar *** para un valor oculto. El PUT no debe escribir ese texto como secreto: la UI omite el campo para conservar el valor existente, envía ${ENV_VAR} para sustituirlo, o lo elimina explícitamente si el campo lo permite. Hay que definir y probar esa semántica junto al handler.
No incluiría POST /config/validate todavía. PUT ya valida antes de escribir y devuelve 422 sin efectos; si más adelante la UI necesita comprobar un borrador sin guardarlo aunque sea válido, se puede añadir ese tercer endpoint reutilizando exactamente el mismo validador. Tampoco incluiría aquí creación de scopes ni edición de settings globales: son operaciones distintas; el owner puede usar este mismo PUT para cualquier scope existente.
