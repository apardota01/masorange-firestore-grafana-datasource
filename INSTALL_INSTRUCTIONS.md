# Firestore Grafana Plugin v0.2.7-timerange

## Nueva Funcionalidad: Filtrado por Rango de Fechas

Esta versión del plugin de Firestore para Grafana incluye soporte para filtrado automático por rango de fechas basado en el selector de tiempo de Grafana.

### Características Nuevas

- **Filtrado Automático por Tiempo**: El plugin ahora respeta el rango de tiempo seleccionado en Grafana
- **Campo de Tiempo Configurable**: Permite especificar qué campo de timestamp usar para el filtrado
- **Compatibilidad Retroactiva**: Funciona igual que antes si no especificas un campo de tiempo

## Instalación

### Método 1: Instalación Manual

1. Detén Grafana si está ejecutándose
2. Extrae el contenido del archivo en el directorio de plugins de Grafana:

```bash
# Para sistemas Linux/macOS
sudo tar -xzf firestore-grafana-datasource-v0.2.7-timerange.tar.gz -C /var/lib/grafana/plugins/firestore/

# O para instalación local
tar -xzf firestore-grafana-datasource-v0.2.7-timerange.tar.gz -C ~/grafana/plugins/firestore/
```

3. Asegúrate de que el binario tenga permisos de ejecución:

```bash
chmod +x /var/lib/grafana/plugins/firestore/gpx_firestore_*
```

4. Reinicia Grafana

### Método 2: Usando grafana-cli (si el plugin estuviera en el marketplace)

```bash
grafana-cli plugins install pgollangi-firestore-datasource
```

## Configuración del Plugin

1. Ve a **Configuration > Data Sources** en Grafana
2. Haz clic en **Add data source**
3. Busca "Firestore" y selecciónalo
4. Configura:
   - **Project ID**: Tu ID de proyecto de Google Cloud
   - **Service Account**: El JSON de tu cuenta de servicio de GCP

## Uso de la Funcionalidad de Rango de Fechas

El plugin ahora soporta **dos métodos** para filtrar por rango de fechas:

### Método 1: Variables Globales de Grafana (Recomendado)

Usa las variables globales `$__from` y `$__to` directamente en tu consulta FireQL:

```sql
-- Ejemplo básico
SELECT * FROM events WHERE timestamp >= $__from AND timestamp <= $__to

-- Con filtros adicionales
SELECT * FROM logs WHERE level = 'error' AND createdAt BETWEEN $__from AND $__to ORDER BY createdAt

-- Con operadores específicos
SELECT * FROM metrics WHERE recordedAt > $__from AND recordedAt < $__to AND value > 100
```

**Ventajas:**
- ✅ Control total sobre la consulta
- ✅ Funciona con cualquier operador SQL (>=, <=, BETWEEN, >, <)
- ✅ Permite múltiples campos de tiempo en la misma consulta
- ✅ Más flexible para consultas complejas

### Método 2: Campo de Tiempo Automático

Si prefieres que el plugin maneje automáticamente el filtrado:

1. **Query**: Escribe tu consulta FireQL normal (ej: `SELECT * FROM users`)
2. **Time Field**: Especifica el nombre del campo que contiene el timestamp (ej: `createdAt`, `timestamp`, `updatedAt`)
3. **Resultado**: El plugin añadirá automáticamente los filtros de tiempo

**Ejemplo Automático:**
- **Consulta Original**: `SELECT * FROM events WHERE type = 'error'`
- **Campo de Tiempo**: `timestamp`
- **Consulta Final**: `SELECT * FROM events WHERE type = 'error' AND (timestamp >= '2023-01-01T00:00:00Z' AND timestamp <= '2023-01-31T23:59:59Z')`

**Nota:** Si tu consulta ya contiene variables `$__from` o `$__to`, el filtrado automático NO se aplicará para evitar duplicación.

### Seleccionar Rango de Tiempo

En ambos métodos, usa el selector de tiempo estándar de Grafana en la parte superior derecha del dashboard.

## Formatos de Campo de Tiempo Soportados

El campo de tiempo debe ser de tipo `timestamp` o `datetime` en Firestore. Los formatos soportados incluyen:
- RFC3339: `2023-01-01T12:00:00Z`
- ISO 8601: `2023-01-01T12:00:00.000Z`
- Timestamps de Firestore nativos

## Compatibilidad

- **Grafana**: >= 9.2.5
- **Sistemas Operativos**: Linux (amd64, arm64), macOS (amd64, arm64), Windows (amd64)
- **Firestore**: Todas las versiones

## Resolución de Problemas

### El filtro de tiempo no funciona
- Verifica que el campo especificado en "Time Field" existe en tu colección
- Asegúrate de que el campo es de tipo timestamp/datetime
- Revisa los logs de Grafana para errores de consulta

### Plugin no aparece en la lista
- Verifica que los archivos se extrajeron en el directorio correcto
- Confirma que el binario tiene permisos de ejecución
- Reinicia Grafana completamente

### Errores de autenticación
- Verifica que tu Service Account tiene permisos de lectura en Firestore
- Confirma que el JSON del Service Account es válido

## Changelog

### v0.2.7-timerange
- ✅ Agregado soporte para variables globales de Grafana `$__from` y `$__to`
- ✅ Agregado soporte para filtrado automático por rango de fechas
- ✅ Nueva interfaz para especificar campo de tiempo
- ✅ Lógica inteligente: evita duplicación cuando se usan variables globales
- ✅ Compatibilidad retroactiva con consultas existentes
- ✅ Tests unitarios exhaustivos para ambos métodos de filtrado

### v0.2.6
- Versión base original

## Soporte

Para reportar problemas o solicitar funcionalidades:
- GitHub: https://github.com/pgollangi/firestore-grafana-datasource
- Issues: https://github.com/pgollangi/firestore-grafana-datasource/issues

## Licencia

MIT License - Ver archivo LICENSE para más detalles.