import { LicenseElement } from '@swagger-api/apidom-ns-openapi-3-0';
class License extends LicenseElement {
  get identifier() {
    return this.get('identifier');
  }
  set identifier(name) {
    this.set('identifier', name);
  }
}
export default License;