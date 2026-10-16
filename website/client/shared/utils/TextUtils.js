export class TextUtils {
  /** @param {string} text @returns {string} */
  static decode(text) {
    return text.replaceAll('&#123;', '{').replaceAll('&#125;', '}')
      .replaceAll('&lt;', '<').replaceAll('&gt;', '>').replaceAll('&amp;', '&')
  }
}
