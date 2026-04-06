// 国家/地区数据
export const countries = [
  // 亚洲 - 东亚
  { code: 'CN', name: '中国', en_name: 'China', flag: '🇨🇳', region: '东亚', en_region: 'East Asia' },
  { code: 'HK', name: '中国香港', en_name: 'Hong Kong', flag: '🇭🇰', region: '东亚', en_region: 'East Asia' },
  { code: 'TW', name: '中国台湾', en_name: 'Taiwan', flag: '🇨🇳', region: '东亚', en_region: 'East Asia' },
  { code: 'MO', name: '中国澳门', en_name: 'Macau', flag: '🇲🇴', region: '东亚', en_region: 'East Asia' },
  { code: 'JP', name: '日本', en_name: 'Japan', flag: '🇯🇵', region: '东亚', en_region: 'East Asia' },
  { code: 'KR', name: '韩国', en_name: 'South Korea', flag: '🇰🇷', region: '东亚', en_region: 'East Asia' },
  { code: 'KP', name: '朝鲜', en_name: 'North Korea', flag: '🇰🇵', region: '东亚', en_region: 'East Asia' },
  { code: 'MN', name: '蒙古', en_name: 'Mongolia', flag: '🇲🇳', region: '东亚', en_region: 'East Asia' },
  
  // 亚洲 - 东南亚
  { code: 'SG', name: '新加坡', en_name: 'Singapore', flag: '🇸🇬', region: '东南亚', en_region: 'Southeast Asia' },
  { code: 'MY', name: '马来西亚', en_name: 'Malaysia', flag: '🇲🇾', region: '东南亚', en_region: 'Southeast Asia' },
  { code: 'TH', name: '泰国', en_name: 'Thailand', flag: '🇹🇭', region: '东南亚', en_region: 'Southeast Asia' },
  { code: 'VN', name: '越南', en_name: 'Vietnam', flag: '🇻🇳', region: '东南亚', en_region: 'Southeast Asia' },
  { code: 'ID', name: '印度尼西亚', en_name: 'Indonesia', flag: '🇮🇩', region: '东南亚', en_region: 'Southeast Asia' },
  { code: 'PH', name: '菲律宾', en_name: 'Philippines', flag: '🇵🇭', region: '东南亚', en_region: 'Southeast Asia' },
  { code: 'BN', name: '文莱', en_name: 'Brunei', flag: '🇧🇳', region: '东南亚', en_region: 'Southeast Asia' },
  { code: 'LA', name: '老挝', en_name: 'Laos', flag: '🇱🇦', region: '东南亚', en_region: 'Southeast Asia' },
  { code: 'KH', name: '柬埔寨', en_name: 'Cambodia', flag: '🇰🇭', region: '东南亚', en_region: 'Southeast Asia' },
  { code: 'MM', name: '缅甸', en_name: 'Myanmar', flag: '🇲🇲', region: '东南亚', en_region: 'Southeast Asia' },
  { code: 'TL', name: '东帝汶', en_name: 'Timor-Leste', flag: '🇹🇱', region: '东南亚', en_region: 'Southeast Asia' },
  
  // 亚洲 - 南亚
  { code: 'IN', name: '印度', en_name: 'India', flag: '🇮🇳', region: '南亚', en_region: 'South Asia' },
  { code: 'PK', name: '巴基斯坦', en_name: 'Pakistan', flag: '🇵🇰', region: '南亚', en_region: 'South Asia' },
  { code: 'BD', name: '孟加拉国', en_name: 'Bangladesh', flag: '🇧🇩', region: '南亚', en_region: 'South Asia' },
  { code: 'LK', name: '斯里兰卡', en_name: 'Sri Lanka', flag: '🇱🇰', region: '南亚', en_region: 'South Asia' },
  { code: 'NP', name: '尼泊尔', en_name: 'Nepal', flag: '🇳🇵', region: '南亚', en_region: 'South Asia' },
  { code: 'BT', name: '不丹', en_name: 'Bhutan', flag: '🇧🇹', region: '南亚', en_region: 'South Asia' },
  { code: 'MV', name: '马尔代夫', en_name: 'Maldives', flag: '🇲🇻', region: '南亚', en_region: 'South Asia' },
  { code: 'AF', name: '阿富汗', en_name: 'Afghanistan', flag: '🇦🇫', region: '南亚', en_region: 'South Asia' },
  
  // 亚洲 - 中亚
  { code: 'KZ', name: '哈萨克斯坦', en_name: 'Kazakhstan', flag: '🇰🇿', region: '中亚', en_region: 'Central Asia' },
  { code: 'UZ', name: '乌兹别克斯坦', en_name: 'Uzbekistan', flag: '🇺🇿', region: '中亚', en_region: 'Central Asia' },
  { code: 'TJ', name: '塔吉克斯坦', en_name: 'Tajikistan', flag: '🇹🇯', region: '中亚', en_region: 'Central Asia' },
  { code: 'KG', name: '吉尔吉斯斯坦', en_name: 'Kyrgyzstan', flag: '🇰🇬', region: '中亚', en_region: 'Central Asia' },
  { code: 'TM', name: '土库曼斯坦', en_name: 'Turkmenistan', flag: '🇹🇲', region: '中亚', en_region: 'Central Asia' },
  
  // 亚洲 - 西亚/中东
  { code: 'TR', name: '土耳其', en_name: 'Turkey', flag: '🇹🇷', region: '西亚/中东', en_region: 'West Asia / Middle East' },
  { code: 'IR', name: '伊朗', en_name: 'Iran', flag: '🇮🇷', region: '西亚/中东', en_region: 'West Asia / Middle East' },
  { code: 'IQ', name: '伊拉克', en_name: 'Iraq', flag: '🇮🇶', region: '西亚/中东', en_region: 'West Asia / Middle East' },
  { code: 'SY', name: '叙利亚', en_name: 'Syria', flag: '🇸🇾', region: '西亚/中东', en_region: 'West Asia / Middle East' },
  { code: 'JO', name: '约旦', en_name: 'Jordan', flag: '🇯🇴', region: '西亚/中东', en_region: 'West Asia / Middle East' },
  { code: 'LB', name: '黎巴嫩', en_name: 'Lebanon', flag: '🇱🇧', region: '西亚/中东', en_region: 'West Asia / Middle East' },
  { code: 'IL', name: '以色列', en_name: 'Israel', flag: '🇮🇱', region: '西亚/中东', en_region: 'West Asia / Middle East' },
  { code: 'SA', name: '沙特阿拉伯', en_name: 'Saudi Arabia', flag: '🇸🇦', region: '西亚/中东', en_region: 'West Asia / Middle East' },
  { code: 'AE', name: '阿联酋', en_name: 'United Arab Emirates', flag: '🇦🇪', region: '西亚/中东', en_region: 'West Asia / Middle East' },
  { code: 'QA', name: '卡塔尔', en_name: 'Qatar', flag: '🇶🇦', region: '西亚/中东', en_region: 'West Asia / Middle East' },
  { code: 'KW', name: '科威特', en_name: 'Kuwait', flag: '🇰🇼', region: '西亚/中东', en_region: 'West Asia / Middle East' },
  { code: 'BH', name: '巴林', en_name: 'Bahrain', flag: '🇧🇭', region: '西亚/中东', en_region: 'West Asia / Middle East' },
  { code: 'OM', name: '阿曼', en_name: 'Oman', flag: '🇴🇲', region: '西亚/中东', en_region: 'West Asia / Middle East' },
  { code: 'YE', name: '也门', en_name: 'Yemen', flag: '🇾🇪', region: '西亚/中东', en_region: 'West Asia / Middle East' },
  { code: 'GE', name: '格鲁吉亚', en_name: 'Georgia', flag: '🇬🇪', region: '西亚/中东', en_region: 'West Asia / Middle East' },
  { code: 'AM', name: '亚美尼亚', en_name: 'Armenia', flag: '🇦🇲', region: '西亚/中东', en_region: 'West Asia / Middle East' },
  { code: 'AZ', name: '阿塞拜疆', en_name: 'Azerbaijan', flag: '🇦🇿', region: '西亚/中东', en_region: 'West Asia / Middle East' },
  { code: 'CY', name: '塞浦路斯', en_name: 'Cyprus', flag: '🇨🇾', region: '西亚/中东', en_region: 'West Asia / Middle East' },
  
  // 欧洲 - 西欧
  { code: 'GB', name: '英国', en_name: 'United Kingdom', flag: '🇬🇧', region: '西欧', en_region: 'Western Europe' },
  { code: 'IE', name: '爱尔兰', en_name: 'Ireland', flag: '🇮🇪', region: '西欧', en_region: 'Western Europe' },
  { code: 'FR', name: '法国', en_name: 'France', flag: '🇫🇷', region: '西欧', en_region: 'Western Europe' },
  { code: 'DE', name: '德国', en_name: 'Germany', flag: '🇩🇪', region: '西欧', en_region: 'Western Europe' },
  { code: 'NL', name: '荷兰', en_name: 'Netherlands', flag: '🇳🇱', region: '西欧', en_region: 'Western Europe' },
  { code: 'BE', name: '比利时', en_name: 'Belgium', flag: '🇧🇪', region: '西欧', en_region: 'Western Europe' },
  { code: 'LU', name: '卢森堡', en_name: 'Luxembourg', flag: '🇱🇺', region: '西欧', en_region: 'Western Europe' },
  { code: 'CH', name: '瑞士', en_name: 'Switzerland', flag: '🇨🇭', region: '西欧', en_region: 'Western Europe' },
  { code: 'AT', name: '奥地利', en_name: 'Austria', flag: '🇦🇹', region: '西欧', en_region: 'Western Europe' },
  { code: 'LI', name: '列支敦士登', en_name: 'Liechtenstein', flag: '🇱🇮', region: '西欧', en_region: 'Western Europe' },
  { code: 'MC', name: '摩纳哥', en_name: 'Monaco', flag: '🇲🇨', region: '西欧', en_region: 'Western Europe' },
  
  // 欧洲 - 北欧
  { code: 'SE', name: '瑞典', en_name: 'Sweden', flag: '🇸🇪', region: '北欧', en_region: 'Northern Europe' },
  { code: 'NO', name: '挪威', en_name: 'Norway', flag: '🇳🇴', region: '北欧', en_region: 'Northern Europe' },
  { code: 'FI', name: '芬兰', en_name: 'Finland', flag: '🇫🇮', region: '北欧', en_region: 'Northern Europe' },
  { code: 'DK', name: '丹麦', en_name: 'Denmark', flag: '🇩🇰', region: '北欧', en_region: 'Northern Europe' },
  { code: 'IS', name: '冰岛', en_name: 'Iceland', flag: '🇮🇸', region: '北欧', en_region: 'Northern Europe' },
  
  // 欧洲 - 南欧
  { code: 'IT', name: '意大利', en_name: 'Italy', flag: '🇮🇹', region: '南欧', en_region: 'Southern Europe' },
  { code: 'ES', name: '西班牙', en_name: 'Spain', flag: '🇪🇸', region: '南欧', en_region: 'Southern Europe' },
  { code: 'PT', name: '葡萄牙', en_name: 'Portugal', flag: '🇵🇹', region: '南欧', en_region: 'Southern Europe' },
  { code: 'GR', name: '希腊', en_name: 'Greece', flag: '🇬🇷', region: '南欧', en_region: 'Southern Europe' },
  { code: 'MT', name: '马耳他', en_name: 'Malta', flag: '🇲🇹', region: '南欧', en_region: 'Southern Europe' },
  { code: 'SM', name: '圣马力诺', en_name: 'San Marino', flag: '🇸🇲', region: '南欧', en_region: 'Southern Europe' },
  { code: 'VA', name: '梵蒂冈', en_name: 'Vatican City', flag: '🇻🇦', region: '南欧', en_region: 'Southern Europe' },
  { code: 'AD', name: '安道尔', en_name: 'Andorra', flag: '🇦🇩', region: '南欧', en_region: 'Southern Europe' },
  { code: 'AL', name: '阿尔巴尼亚', en_name: 'Albania', flag: '🇦🇱', region: '南欧', en_region: 'Southern Europe' },
  { code: 'RS', name: '塞尔维亚', en_name: 'Serbia', flag: '🇷🇸', region: '南欧', en_region: 'Southern Europe' },
  { code: 'ME', name: '黑山', en_name: 'Montenegro', flag: '🇲🇪', region: '南欧', en_region: 'Southern Europe' },
  { code: 'BA', name: '波黑', en_name: 'Bosnia and Herzegovina', flag: '🇧🇦', region: '南欧', en_region: 'Southern Europe' },
  { code: 'HR', name: '克罗地亚', en_name: 'Croatia', flag: '🇭🇷', region: '南欧', en_region: 'Southern Europe' },
  { code: 'SI', name: '斯洛文尼亚', en_name: 'Slovenia', flag: '🇸🇮', region: '南欧', en_region: 'Southern Europe' },
  { code: 'MK', name: '北马其顿', en_name: 'North Macedonia', flag: '🇲🇰', region: '南欧', en_region: 'Southern Europe' },
  
  // 欧洲 - 中欧
  { code: 'PL', name: '波兰', en_name: 'Poland', flag: '🇵🇱', region: '中欧', en_region: 'Central Europe' },
  { code: 'CZ', name: '捷克', en_name: 'Czech Republic', flag: '🇨🇿', region: '中欧', en_region: 'Central Europe' },
  { code: 'SK', name: '斯洛伐克', en_name: 'Slovakia', flag: '🇸🇰', region: '中欧', en_region: 'Central Europe' },
  { code: 'HU', name: '匈牙利', en_name: 'Hungary', flag: '🇭🇺', region: '中欧', en_region: 'Central Europe' },
  
  // 欧洲 - 东欧
  { code: 'RU', name: '俄罗斯', en_name: 'Russia', flag: '🇷🇺', region: '东欧', en_region: 'Eastern Europe' },
  { code: 'UA', name: '乌克兰', en_name: 'Ukraine', flag: '🇺🇦', region: '东欧', en_region: 'Eastern Europe' },
  { code: 'BY', name: '白俄罗斯', en_name: 'Belarus', flag: '🇧🇾', region: '东欧', en_region: 'Eastern Europe' },
  { code: 'MD', name: '摩尔多瓦', en_name: 'Moldova', flag: '🇲🇩', region: '东欧', en_region: 'Eastern Europe' },
  { code: 'RO', name: '罗马尼亚', en_name: 'Romania', flag: '🇷🇴', region: '东欧', en_region: 'Eastern Europe' },
  { code: 'BG', name: '保加利亚', en_name: 'Bulgaria', flag: '🇧🇬', region: '东欧', en_region: 'Eastern Europe' },
  { code: 'EE', name: '爱沙尼亚', en_name: 'Estonia', flag: '🇪🇪', region: '东欧', en_region: 'Eastern Europe' },
  { code: 'LV', name: '拉脱维亚', en_name: 'Latvia', flag: '🇱🇻', region: '东欧', en_region: 'Eastern Europe' },
  { code: 'LT', name: '立陶宛', en_name: 'Lithuania', flag: '🇱🇹', region: '东欧', en_region: 'Eastern Europe' },
  
  // 北美洲
  { code: 'US', name: '美国', en_name: 'United States', flag: '🇺🇸', region: '北美洲', en_region: 'North America' },
  { code: 'CA', name: '加拿大', en_name: 'Canada', flag: '🇨🇦', region: '北美洲', en_region: 'North America' },
  { code: 'MX', name: '墨西哥', en_name: 'Mexico', flag: '🇲🇽', region: '北美洲', en_region: 'North America' },
  { code: 'GT', name: '危地马拉', en_name: 'Guatemala', flag: '🇬🇹', region: '北美洲', en_region: 'North America' },
  { code: 'BZ', name: '伯利兹', en_name: 'Belize', flag: '🇧🇿', region: '北美洲', en_region: 'North America' },
  { code: 'SV', name: '萨尔瓦多', en_name: 'El Salvador', flag: '🇸🇻', region: '北美洲', en_region: 'North America' },
  { code: 'HN', name: '洪都拉斯', en_name: 'Honduras', flag: '🇭🇳', region: '北美洲', en_region: 'North America' },
  { code: 'NI', name: '尼加拉瓜', en_name: 'Nicaragua', flag: '🇳🇮', region: '北美洲', en_region: 'North America' },
  { code: 'CR', name: '哥斯达黎加', en_name: 'Costa Rica', flag: '🇨🇷', region: '北美洲', en_region: 'North America' },
  { code: 'PA', name: '巴拿马', en_name: 'Panama', flag: '🇵🇦', region: '北美洲', en_region: 'North America' },
  { code: 'CU', name: '古巴', en_name: 'Cuba', flag: '🇨🇺', region: '加勒比海', en_region: 'Caribbean' },
  { code: 'JM', name: '牙买加', en_name: 'Jamaica', flag: '🇯🇲', region: '加勒比海', en_region: 'Caribbean' },
  { code: 'HT', name: '海地', en_name: 'Haiti', flag: '🇭🇹', region: '加勒比海', en_region: 'Caribbean' },
  { code: 'DO', name: '多米尼加', en_name: 'Dominican Republic', flag: '🇩🇴', region: '加勒比海', en_region: 'Caribbean' },
  { code: 'BS', name: '巴哈马', en_name: 'Bahamas', flag: '🇧🇸', region: '加勒比海', en_region: 'Caribbean' },
  { code: 'BB', name: '巴巴多斯', en_name: 'Barbados', flag: '🇧🇧', region: '加勒比海', en_region: 'Caribbean' },
  { code: 'TT', name: '特立尼达和多巴哥', en_name: 'Trinidad and Tobago', flag: '��', region: '加勒比海', en_region: 'Caribbean' },
  
  // 南美洲
  { code: 'BR', name: '巴西', en_name: 'Brazil', flag: '🇧🇷', region: '南美洲', en_region: 'South America' },
  { code: 'AR', name: '阿根廷', en_name: 'Argentina', flag: '🇦🇷', region: '南美洲', en_region: 'South America' },
  { code: 'CL', name: '智利', en_name: 'Chile', flag: '🇨🇱', region: '南美洲', en_region: 'South America' },
  { code: 'CO', name: '哥伦比亚', en_name: 'Colombia', flag: '🇨🇴', region: '南美洲', en_region: 'South America' },
  { code: 'PE', name: '秘鲁', en_name: 'Peru', flag: '🇵🇪', region: '南美洲', en_region: 'South America' },
  { code: 'VE', name: '委内瑞拉', en_name: 'Venezuela', flag: '🇻🇪', region: '南美洲', en_region: 'South America' },
  { code: 'EC', name: '厄瓜多尔', en_name: 'Ecuador', flag: '🇪🇨', region: '南美洲', en_region: 'South America' },
  { code: 'BO', name: '玻利维亚', en_name: 'Bolivia', flag: '🇧🇴', region: '南美洲', en_region: 'South America' },
  { code: 'PY', name: '巴拉圭', en_name: 'Paraguay', flag: '🇵🇾', region: '南美洲', en_region: 'South America' },
  { code: 'UY', name: '乌拉圭', en_name: 'Uruguay', flag: '🇺🇾', region: '南美洲', en_region: 'South America' },
  { code: 'GY', name: '圭亚那', en_name: 'Guyana', flag: '🇬🇾', region: '南美洲', en_region: 'South America' },
  { code: 'SR', name: '苏里南', en_name: 'Suriname', flag: '🇸🇷', region: '南美洲', en_region: 'South America' },
  { code: 'GF', name: '法属圭亚那', en_name: 'French Guiana', flag: '🇬🇫', region: '南美洲', en_region: 'South America' },
  
  // 非洲 - 北非
  { code: 'EG', name: '埃及', en_name: 'Egypt', flag: '🇪🇬', region: '北非', en_region: 'North Africa' },
  { code: 'LY', name: '利比亚', en_name: 'Libya', flag: '🇱🇾', region: '北非', en_region: 'North Africa' },
  { code: 'TN', name: '突尼斯', en_name: 'Tunisia', flag: '🇹🇳', region: '北非', en_region: 'North Africa' },
  { code: 'DZ', name: '阿尔及利亚', en_name: 'Algeria', flag: '🇩🇿', region: '北非', en_region: 'North Africa' },
  { code: 'MA', name: '摩洛哥', en_name: 'Morocco', flag: '🇲🇦', region: '北非', en_region: 'North Africa' },
  { code: 'SD', name: '苏丹', en_name: 'Sudan', flag: '🇸🇩', region: '北非', en_region: 'North Africa' },
  { code: 'SS', name: '南苏丹', en_name: 'South Sudan', flag: '🇸🇸', region: '北非', en_region: 'North Africa' },
  
  // 非洲 - 西非
  { code: 'NG', name: '尼日利亚', en_name: 'Nigeria', flag: '🇳🇬', region: '西非', en_region: 'West Africa' },
  { code: 'GH', name: '加纳', en_name: 'Ghana', flag: '🇬🇭', region: '西非', en_region: 'West Africa' },
  { code: 'CI', name: '科特迪瓦', en_name: 'Ivory Coast', flag: '🇨🇮', region: '西非', en_region: 'West Africa' },
  { code: 'SN', name: '塞内加尔', en_name: 'Senegal', flag: '🇸🇳', region: '西非', en_region: 'West Africa' },
  { code: 'ML', name: '马里', en_name: 'Mali', flag: '🇲🇱', region: '西非', en_region: 'West Africa' },
  { code: 'BF', name: '布基纳法索', en_name: 'Burkina Faso', flag: '🇧🇫', region: '西非', en_region: 'West Africa' },
  { code: 'NE', name: '尼日尔', en_name: 'Niger', flag: '🇳🇪', region: '西非', en_region: 'West Africa' },
  { code: 'GN', name: '几内亚', en_name: 'Guinea', flag: '🇬🇳', region: '西非', en_region: 'West Africa' },
  { code: 'SL', name: '塞拉利昂', en_name: 'Sierra Leone', flag: '🇸🇱', region: '西非', en_region: 'West Africa' },
  { code: 'LR', name: '利比里亚', en_name: 'Liberia', flag: '🇱🇷', region: '西非', en_region: 'West Africa' },
  { code: 'TG', name: '多哥', en_name: 'Togo', flag: '🇹🇬', region: '西非', en_region: 'West Africa' },
  { code: 'BJ', name: '贝宁', en_name: 'Benin', flag: '🇧🇯', region: '西非', en_region: 'West Africa' },
  
  // 非洲 - 中非
  { code: 'TD', name: '乍得', en_name: 'Chad', flag: '🇹🇩', region: '中非', en_region: 'Central Africa' },
  { code: 'CF', name: '中非共和国', en_name: 'Central African Republic', flag: '🇨🇫', region: '中非', en_region: 'Central Africa' },
  { code: 'CM', name: '喀麦隆', en_name: 'Cameroon', flag: '🇨🇲', region: '中非', en_region: 'Central Africa' },
  { code: 'GQ', name: '赤道几内亚', en_name: 'Equatorial Guinea', flag: '🇬🇶', region: '中非', en_region: 'Central Africa' },
  { code: 'GA', name: '加蓬', en_name: 'Gabon', flag: '🇬🇦', region: '中非', en_region: 'Central Africa' },
  { code: 'CG', name: '刚果(布)', en_name: 'Republic of the Congo', flag: '🇨🇬', region: '中非', en_region: 'Central Africa' },
  { code: 'CD', name: '刚果(金)', en_name: 'Democratic Republic of the Congo', flag: '🇨🇩', region: '中非', en_region: 'Central Africa' },
  { code: 'AO', name: '安哥拉', en_name: 'Angola', flag: '🇦🇴', region: '中非', en_region: 'Central Africa' },
  
  // 非洲 - 东非
  { code: 'ET', name: '埃塞俄比亚', en_name: 'Ethiopia', flag: '🇪🇹', region: '东非', en_region: 'East Africa' },
  { code: 'KE', name: '肯尼亚', en_name: 'Kenya', flag: '🇰🇪', region: '东非', en_region: 'East Africa' },
  { code: 'UG', name: '乌干达', en_name: 'Uganda', flag: '🇺🇬', region: '东非', en_region: 'East Africa' },
  { code: 'TZ', name: '坦桑尼亚', en_name: 'Tanzania', flag: '🇹🇿', region: '东非', en_region: 'East Africa' },
  { code: 'RW', name: '卢旺达', en_name: 'Rwanda', flag: '🇷🇼', region: '东非', en_region: 'East Africa' },
  { code: 'BI', name: '布隆迪', en_name: 'Burundi', flag: '🇧🇮', region: '东非', en_region: 'East Africa' },
  { code: 'SO', name: '索马里', en_name: 'Somalia', flag: '🇸🇴', region: '东非', en_region: 'East Africa' },
  { code: 'DJ', name: '吉布提', en_name: 'Djibouti', flag: '🇩🇯', region: '东非', en_region: 'East Africa' },
  { code: 'ER', name: '厄立特里亚', en_name: 'Eritrea', flag: '🇪🇷', region: '东非', en_region: 'East Africa' },
  { code: 'MG', name: '马达加斯加', en_name: 'Madagascar', flag: '🇲🇬', region: '东非', en_region: 'East Africa' },
  { code: 'MU', name: '毛里求斯', en_name: 'Mauritius', flag: '🇲🇺', region: '东非', en_region: 'East Africa' },
  { code: 'SC', name: '塞舌尔', en_name: 'Seychelles', flag: '🇸🇨', region: '东非', en_region: 'East Africa' },
  { code: 'KM', name: '科摩罗', en_name: 'Comoros', flag: '🇰🇲', region: '东非', en_region: 'East Africa' },
  
  // 非洲 - 南非
  { code: 'ZA', name: '南非', en_name: 'South Africa', flag: '🇿🇦', region: '南部非洲', en_region: 'Southern Africa' },
  { code: 'ZW', name: '津巴布韦', en_name: 'Zimbabwe', flag: '🇿🇼', region: '南部非洲', en_region: 'Southern Africa' },
  { code: 'ZM', name: '赞比亚', en_name: 'Zambia', flag: '🇿🇲', region: '南部非洲', en_region: 'Southern Africa' },
  { code: 'MW', name: '马拉维', en_name: 'Malawi', flag: '🇲🇼', region: '南部非洲', en_region: 'Southern Africa' },
  { code: 'MZ', name: '莫桑比克', en_name: 'Mozambique', flag: '🇲🇿', region: '南部非洲', en_region: 'Southern Africa' },
  { code: 'BW', name: '博茨瓦纳', en_name: 'Botswana', flag: '🇧🇼', region: '南部非洲', en_region: 'Southern Africa' },
  { code: 'NA', name: '纳米比亚', en_name: 'Namibia', flag: '🇳🇦', region: '南部非洲', en_region: 'Southern Africa' },
  { code: 'LS', name: '莱索托', en_name: 'Lesotho', flag: '🇱🇸', region: '南部非洲', en_region: 'Southern Africa' },
  { code: 'SZ', name: '斯威士兰', en_name: 'Eswatini', flag: '🇸🇿', region: '南部非洲', en_region: 'Southern Africa' },
  
  // 大洋洲
  { code: 'AU', name: '澳大利亚', en_name: 'Australia', flag: '🇦🇺', region: '大洋洲', en_region: 'Oceania' },
  { code: 'NZ', name: '新西兰', en_name: 'New Zealand', flag: '🇳🇿', region: '大洋洲', en_region: 'Oceania' },
  { code: 'FJ', name: '斐济', en_name: 'Fiji', flag: '🇫🇯', region: '大洋洲', en_region: 'Oceania' },
  { code: 'PG', name: '巴布亚新几内亚', en_name: 'Papua New Guinea', flag: '🇵🇬', region: '大洋洲', en_region: 'Oceania' },
  { code: 'SB', name: '所罗门群岛', en_name: 'Solomon Islands', flag: '🇸🇧', region: '大洋洲', en_region: 'Oceania' },
  { code: 'VU', name: '瓦努阿图', en_name: 'Vanuatu', flag: '🇻🇺', region: '大洋洲', en_region: 'Oceania' },
  { code: 'NC', name: '新喀里多尼亚', en_name: 'New Caledonia', flag: '🇳🇨', region: '大洋洲', en_region: 'Oceania' },
  { code: 'PF', name: '法属波利尼西亚', en_name: 'French Polynesia', flag: '🇵🇫', region: '大洋洲', en_region: 'Oceania' },
  { code: 'WS', name: '萨摩亚', en_name: 'Samoa', flag: '🇼🇸', region: '大洋洲', en_region: 'Oceania' },
  { code: 'TO', name: '汤加', en_name: 'Tonga', flag: '🇹🇴', region: '大洋洲', en_region: 'Oceania' },
  { code: 'CK', name: '库克群岛', en_name: 'Cook Islands', flag: '🇨🇰', region: '大洋洲', en_region: 'Oceania' },
  { code: 'NU', name: '纽埃', en_name: 'Niue', flag: '🇳🇺', region: '大洋洲', en_region: 'Oceania' },
  { code: 'PW', name: '帕劳', en_name: 'Palau', flag: '🇵🇼', region: '大洋洲', en_region: 'Oceania' },
  { code: 'FM', name: '密克罗尼西亚', en_name: 'Micronesia', flag: '🇫🇲', region: '大洋洲', en_region: 'Oceania' },
  { code: 'MH', name: '马绍尔群岛', en_name: 'Marshall Islands', flag: '🇲🇭', region: '大洋洲', en_region: 'Oceania' },
  { code: 'KI', name: '基里巴斯', en_name: 'Kiribati', flag: '🇰🇮', region: '大洋洲', en_region: 'Oceania' },
  { code: 'NR', name: '瑙鲁', en_name: 'Nauru', flag: '🇳🇷', region: '大洋洲', en_region: 'Oceania' },
  { code: 'TV', name: '图瓦卢', en_name: 'Tuvalu', flag: '🇹🇻', region: '大洋洲', en_region: 'Oceania' }
]

// 根据国家代码获取国旗
export function getFlagEmoji(countryCode) {
  const country = countries.find(c => c.code === countryCode)
  return country ? country.flag : '🌍'
}

// 根据国家代码获取国家信息
export function getCountryInfo(countryCode) {
  return countries.find(c => c.code === countryCode)
}

// 根据 locale 获取本地化国家名称
export function getLocalizedName(country, locale = 'zh') {
  if (!country) return ''
  return locale === 'en' ? (country.en_name || country.name) : country.name
}

// 根据 locale 获取本地化地区名称
export function getLocalizedRegion(country, locale = 'zh') {
  if (!country) return ''
  return locale === 'en' ? (country.en_region || country.region) : country.region
}

// 根据国家名称获取国家信息（支持中英文搜索）
export function getCountryByName(countryName) {
  return countries.find(c => c.name === countryName || c.en_name === countryName)
}

// 按地区分组（支持 locale 参数）
export function getCountriesByRegion(locale = 'zh') {
  const grouped = {}
  countries.forEach(country => {
    const regionKey = locale === 'en' ? (country.en_region || country.region) : country.region
    if (!grouped[regionKey]) {
      grouped[regionKey] = []
    }
    grouped[regionKey].push(country)
  })
  return grouped
}
